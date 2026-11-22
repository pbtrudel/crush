//go:build linux

package netjail_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestMain doubles as the jail's entry point: the integration test re-execs
// this test binary through Wrap, and the child runs the probe below.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__jail" {
		os.Exit(netjail.Main(os.Args[2:]))
	}
	// The availability check runs the jail with no command to run.
	if os.Getenv("NETJAIL_NOOP") == "1" {
		os.Exit(0)
	}
	if os.Getenv("NETJAIL_PROBE") == "1" {
		os.Exit(probe())
	}
	os.Exit(m.Run())
}

// canJail reports whether this machine lets an unprivileged process start
// the jail, and why not when it does not.
func canJail(t *testing.T) (bool, string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "NETJAIL_NOOP=1")
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Sprintf("%v: %s", err, out)
	}
	return true, ""
}

// probe runs inside the jail and checks that the namespace has only a
// working loopback: no route out, a 127.0.0.1 listener that works, and the
// forwarded proxy port that reaches the allowlisted test server.
func probe() int {
	fail := func(format string, args ...any) int {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return 1
	}

	// go writes its test binaries under the masked /tmp and runs them, so a
	// tmpfs mount there must not be noexec.
	if os.Getenv("NETJAIL_EXEC_TMP") != "" {
		path := "/tmp/exec-probe.sh"
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho exec-tmp-ok\n"), 0o700); err != nil {
			return fail("write the probe: %v", err)
		}
		out, err := exec.CommandContext(context.Background(), path).CombinedOutput()
		if err != nil {
			return fail("run the probe: %v: %s", err, out)
		}
		fmt.Print(string(out))
		return 0
	}

	// The identity probe reports the uid, gid and capabilities the child
	// really has after exec.
	if os.Getenv("NETJAIL_IDENTITY") != "" {
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			return fail("read status: %v", err)
		}
		fmt.Printf("uid=%d gid=%d %s %s\n", os.Getuid(), os.Getgid(), capLine(status, "CapEff"), capLine(status, "CapAmb"))
		return 0
	}

	// A masked directory hides a unix socket the host can still reach.
	if masked := os.Getenv("NETJAIL_MASK_SOCK"); masked != "" {
		conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", masked)
		if err == nil {
			conn.Close()
			return fail("the masked socket %s was reachable", masked)
		}
		fmt.Println("masked ok")
		return 0
	}

	// The masks hide the entries below them, and whatever the run keeps
	// stays readable and writable. A masked path that no longer exists is
	// reported as absent: a mask over its parent hid it.
	if dirs := os.Getenv("NETJAIL_LIST"); dirs != "" {
		for _, dir := range strings.Split(dirs, ",") {
			entries, err := os.ReadDir(dir)
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Printf("absent %s\n", filepath.Base(dir))
				continue
			}
			if err != nil {
				return fail("read %s: %v", dir, err)
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			fmt.Printf("entries %s=%s\n", filepath.Base(dir), strings.Join(names, ","))
		}
		if read := os.Getenv("NETJAIL_KEEP_READ"); read != "" {
			data, err := os.ReadFile(read)
			if err != nil {
				return fail("read %s: %v", read, err)
			}
			fmt.Printf("content=%s\n", strings.TrimSpace(string(data)))
		}
		if write := os.Getenv("NETJAIL_KEEP_WRITE"); write != "" {
			if err := os.WriteFile(write, []byte("written\n"), 0o600); err != nil {
				return fail("write %s: %v", write, err)
			}
			fmt.Println("write ok")
		}
		return 0
	}

	iface, err := net.InterfaceByName("lo")
	if err != nil {
		return fail("loopback is missing: %v", err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return fail("loopback is down")
	}
	if iface.Flags&net.FlagLoopback == 0 {
		return fail("lo is not a loopback interface")
	}

	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(context.Background(), "tcp", "192.0.2.1:80")
	if err == nil {
		conn.Close()
		return fail("a non-loopback address was reachable")
	}

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return fail("listen on loopback: %v", err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()
	local, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		return fail("dial loopback: %v", err)
	}
	defer local.Close()
	if _, err := local.Write([]byte("ping")); err != nil {
		return fail("write loopback: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(local, got); err != nil {
		return fail("read loopback: %v", err)
	}
	if string(got) != "ping" {
		return fail("loopback echoed %q", got)
	}
	// The echo goroutine ends when the client half closes.
	_ = local.Close()
	wg.Wait()

	allow := os.Getenv("NETJAIL_ALLOW")
	fwd := os.Getenv("NETJAIL_FWD_PORT")
	proxied, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", "127.0.0.1:"+fwd)
	if err != nil {
		return fail("dial forwarded port: %v", err)
	}
	defer proxied.Close()
	if _, err := fmt.Fprintf(proxied, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", allow, allow); err != nil {
		return fail("write CONNECT: %v", err)
	}
	br := bufio.NewReader(proxied)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fail("read CONNECT response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("CONNECT through the jail: %s", resp.Status)
	}
	if _, err := fmt.Fprintf(proxied, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", allow); err != nil {
		return fail("write GET: %v", err)
	}
	resp, err = http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return fail("read GET response: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fail("read GET body: %v", err)
	}
	if string(body) != "served\n" {
		return fail("tunnelled server answered %q", body)
	}
	fmt.Println("probe ok")
	return 0
}

// TestJail runs a probe inside the network jail: loopback is up, nothing
// else is reachable, and the forwarded allowlisted port reaches the test
// server.
func TestJail(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "served")
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := filepath.Join(t.TempDir(), "net.sock")
	p, err := netjail.Listen(ctx, sock, []string{srv.Listener.Addr().String()})
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.
	port := netjail.ProxyPort

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	// The run sets the process group before it wraps the command, so the
	// test takes the same path.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"NETJAIL_PROBE=1",
		"NETJAIL_ALLOW="+srv.Listener.Addr().String(),
		"NETJAIL_FWD_PORT="+strconv.Itoa(port),
	)
	require.NoError(t, netjail.Wrap(cmd, sock, port))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "probe ok")

	denied, hosts := p.Denied()
	require.Zero(t, denied)
	require.Empty(t, hosts)
}

// capLine returns the "CapXxx:<hex>" field of /proc/self/status.
func capLine(status []byte, name string) string {
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, name+":") {
			fields := strings.Fields(line)
			return strings.TrimSuffix(fields[0], ":") + "=" + fields[1]
		}
	}
	return name + "=missing"
}

// TestJailExecInTmp checks that the masked /tmp stays executable, since go
// writes its test binaries there and runs them.
func TestJailExecInTmp(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "NETJAIL_PROBE=1", "NETJAIL_EXEC_TMP=1")
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "exec-tmp-ok")
}

// TestPreflight checks that a machine that can start the jail passes the
// preflight run and batch use before a batch starts.
func TestPreflight(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}
	require.NoError(t, netjail.Preflight())
}

// TestJailIdentity checks that the child keeps the host uid and gid and has
// no capabilities, effective or ambient.
func TestJailIdentity(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "NETJAIL_PROBE=1", "NETJAIL_IDENTITY=1")
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), fmt.Sprintf("uid=%d gid=%d", os.Getuid(), os.Getgid()))
	require.Contains(t, string(out), "CapEff=0000000000000000")
	require.Contains(t, string(out), "CapAmb=0000000000000000")
}

// TestJailMasksSockets checks that a directory the jail masks with an empty
// tmpfs hides a unix socket that the host can still reach.
func TestJailMasksSockets(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "agent.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	defer ln.Close()

	// The socket answers the host, so the child's failure is the mask, not
	// a missing listener.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "NETJAIL_PROBE=1", "NETJAIL_MASK_SOCK="+path)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: dir}))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "masked ok")
}

// TestJailMaskKeepsTheRunDirectory checks a mask with a kept path, in the
// layout of the eval home's batches: the masked directory reads as empty,
// the run's own directory stays readable and writable, and the host still
// sees both.
func TestJailMaskKeepsTheRunDirectory(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	root := t.TempDir()
	batches := filepath.Join(root, "batches")
	sibling := filepath.Join(batches, "other", "runs", "pr-0010")
	run := filepath.Join(batches, "b1", "runs", "pr-0010")
	require.NoError(t, os.MkdirAll(sibling, 0o700))
	require.NoError(t, os.MkdirAll(run, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "results.jsonl"), []byte("secret\n"), 0o600))
	kept := filepath.Join(run, "src", "pkg.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(kept), 0o700))
	require.NoError(t, os.WriteFile(kept, []byte("kept\n"), 0o600))
	written := filepath.Join(run, "written.txt")

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(),
		"NETJAIL_PROBE=1",
		"NETJAIL_LIST="+batches,
		"NETJAIL_KEEP_READ="+kept,
		"NETJAIL_KEEP_WRITE="+written,
	)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: batches, Keep: []string{run}}))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "entries batches=b1", "the masked batches hold only the run's own batch")
	require.Contains(t, string(out), "content=kept")
	require.Contains(t, string(out), "write ok")
	// The kept path is the host directory itself, so a child's write lands
	// in it, and the host still sees what the jail hid.
	data, err := os.ReadFile(written)
	require.NoError(t, err)
	require.Equal(t, "written\n", string(data))
	require.FileExists(t, filepath.Join(sibling, "results.jsonl"))
}

// TestJailMaskThroughASymlinkedHome checks that a batches mask named through
// a symlinked eval home also hides the siblings at the symlink's target, and
// the other way round: the eval home is often a symlink to a larger disk.
func TestJailMaskThroughASymlinkedHome(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	root := t.TempDir()
	real := filepath.Join(root, "disk", "crush-eval")
	link := filepath.Join(root, "state", "crush-eval")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "batches", "b1", "runs", "pr-0010-r2"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(real, "batches", "b1", "runs", "pr-0010-r1"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o700))
	require.NoError(t, os.Symlink(real, link))

	for _, tc := range []struct{ name, maskHome, listHome string }{
		{"mask link list real", link, real},
		{"mask link list link", link, link},
		{"mask real list link", real, link},
	} {
		self, err := os.Executable()
		require.NoError(t, err)
		run := filepath.Join(tc.maskHome, "batches", "b1", "runs", "pr-0010-r1")
		cmd := exec.CommandContext(context.Background(), self)
		cmd.Env = append(os.Environ(),
			"NETJAIL_PROBE=1",
			"NETJAIL_LIST="+filepath.Join(tc.listHome, "batches", "b1", "runs"),
		)
		require.NoError(t, netjail.Wrap(cmd, "", 0,
			netjail.Mask{Path: filepath.Join(tc.maskHome, "batches"), Keep: []string{run}}))
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, tc.name+": "+string(out))
		require.Contains(t, string(out), "entries runs=pr-0010-r1", tc.name+": "+string(out))
		require.NotContains(t, string(out), "pr-0010-r2", tc.name)
	}
}

// TestWrapMergesSysProcAttr checks that Wrap keeps the process group the
// run sets and adds the two namespaces and the identity mappings.
func TestWrapMergesSysProcAttr(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(context.Background(), "true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, netjail.Wrap(cmd, "/run/net.sock", 1234))

	self, err := os.Executable()
	require.NoError(t, err)
	require.Equal(t, self, cmd.Path)
	require.Equal(t, []string{self, "__jail", "--sock", "/run/net.sock", "--port", "1234", "--", "true"}, cmd.Args)
	require.True(t, cmd.SysProcAttr.Setpgid)
	require.NotZero(t, cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWUSER)
	require.NotZero(t, cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNET)
	require.NotZero(t, cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNS)
	require.Len(t, cmd.SysProcAttr.UidMappings, 1)
	require.Len(t, cmd.SysProcAttr.GidMappings, 1)
	require.Equal(t, os.Getuid(), cmd.SysProcAttr.UidMappings[0].ContainerID)
	require.Equal(t, os.Getuid(), cmd.SysProcAttr.UidMappings[0].HostID)
	require.Equal(t, os.Getgid(), cmd.SysProcAttr.GidMappings[0].ContainerID)
	require.Equal(t, os.Getgid(), cmd.SysProcAttr.GidMappings[0].HostID)
	require.False(t, cmd.SysProcAttr.GidMappingsEnableSetgroups)
	require.Contains(t, cmd.SysProcAttr.AmbientCaps, uintptr(unix.CAP_NET_ADMIN))
	require.Contains(t, cmd.SysProcAttr.AmbientCaps, uintptr(unix.CAP_SYS_ADMIN))
}

// TestWrapWithoutProxy checks the grading command line: no socket, no port.
func TestWrapWithoutProxy(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(context.Background(), "true")
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	self, err := os.Executable()
	require.NoError(t, err)
	require.Equal(t, []string{self, "__jail", "--", "true"}, cmd.Args)
}

// TestJailCancelKillsGroup checks that a cancelled jail takes its child and
// the child's process group with it instead of waiting for them.
func TestJailCancelKillsGroup(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sleep", "30")
	netjail.KillGroup(cmd)
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled jail did not exit")
	}
	require.Eventually(t, func() bool {
		return syscall.Kill(-pid, 0) == syscall.ESRCH
	}, 2*time.Second, 10*time.Millisecond, "the jailed process group survived")
}

// TestJailExitCode checks that the jail returns the child's exit code.
func TestJailExitCode(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 7")
	require.NoError(t, netjail.Wrap(cmd, "", 0))
	err := cmd.Run()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 7, exit.ExitCode())
}

// TestJailRefusesToMaskTheBinary checks that a mask the caller asked for
// that holds the binary the jail runs stops the jail with a message, instead
// of running the child with that mask dropped.
func TestJailRefusesToMaskTheBinary(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: filepath.Dir(self)}))
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "the jail must not run unmasked")
	require.Contains(t, string(out), "Netjail: refuse")
	require.Contains(t, string(out), filepath.Dir(self))
}

// TestJailNestedMasks checks two masks, one inside the other like a worktrees
// parent and the checkout under it: neither exposes what the host sees, in
// either order, and the host keeps both.
func TestJailNestedMasks(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	root := t.TempDir()
	parent := filepath.Join(root, "worktrees")
	checkout := filepath.Join(parent, "eval-jail-fs")
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, "internal"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "internal", "x.go"), []byte("answer\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(parent, "other"), 0o700))

	tests := []struct {
		name  string
		masks []netjail.Mask
	}{
		{name: "the checkout masked first", masks: []netjail.Mask{{Path: checkout}, {Path: parent}}},
		{name: "the parent masked first", masks: []netjail.Mask{{Path: parent}, {Path: checkout}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			self, err := os.Executable()
			require.NoError(t, err)
			cmd := exec.CommandContext(context.Background(), self)
			cmd.Env = append(os.Environ(),
				"NETJAIL_PROBE=1",
				"NETJAIL_LIST="+parent+","+checkout,
			)
			require.NoError(t, netjail.Wrap(cmd, "", 0, test.masks...))
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			require.Contains(t, string(out), "entries worktrees=", "the parent reads empty")
			require.Contains(t, string(out), "absent eval-jail-fs", "the checkout is unreachable")
			require.FileExists(t, filepath.Join(checkout, "internal", "x.go"),
				"the host still sees the checkout")
		})
	}
}

// TestJailMaskFailure checks that a mask the jail cannot mount stops it with a
// Netjail message and a nonzero exit, instead of running the child unmasked.
func TestJailMaskFailure(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: file}))
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.NotZero(t, cmd.ProcessState.ExitCode())
	require.Contains(t, string(out), "Netjail: mask")
}

// TestJailKeepOutsideEveryMask checks a kept path that no mask covers: the
// child still reads and writes it, and the mask beside it is empty.
func TestJailKeepOutsideEveryMask(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	root := t.TempDir()
	masked := filepath.Join(root, "masked")
	visible := filepath.Join(root, "visible")
	require.NoError(t, os.MkdirAll(masked, 0o700))
	require.NoError(t, os.MkdirAll(visible, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(masked, "secret.txt"), []byte("secret\n"), 0o600))
	kept := filepath.Join(visible, "keep.txt")
	require.NoError(t, os.WriteFile(kept, []byte("kept\n"), 0o600))
	written := filepath.Join(visible, "written.txt")

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(),
		"NETJAIL_PROBE=1",
		"NETJAIL_LIST="+masked,
		"NETJAIL_KEEP_READ="+kept,
		"NETJAIL_KEEP_WRITE="+written,
	)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: masked, Keep: []string{visible}}))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "entries masked=")
	require.Contains(t, string(out), "content=kept")
	require.Contains(t, string(out), "write ok")
	require.FileExists(t, written)
}

// TestJailKeepThatDoesNotExist checks a kept path that is missing: the jail
// skips it, the mask still hides its directory, and the run succeeds.
func TestJailKeepThatDoesNotExist(t *testing.T) {
	t.Parallel()

	ok, reason := canJail(t)
	if !ok {
		t.Skipf("user namespaces are unavailable: %s", reason)
	}

	root := t.TempDir()
	masked := filepath.Join(root, "masked")
	require.NoError(t, os.MkdirAll(filepath.Join(masked, "run"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(masked, "secret.txt"), []byte("secret\n"), 0o600))
	missing := filepath.Join(masked, "no-such-run")

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "NETJAIL_PROBE=1", "NETJAIL_LIST="+masked)
	require.NoError(t, netjail.Wrap(cmd, "", 0, netjail.Mask{Path: masked, Keep: []string{missing}}))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "entries masked=", "a missing keep is skipped, not an error")
	require.FileExists(t, filepath.Join(masked, "secret.txt"))
}

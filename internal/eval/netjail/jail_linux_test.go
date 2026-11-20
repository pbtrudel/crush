//go:build linux

package netjail_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
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
	port, err := netjail.Port()
	require.NoError(t, err)

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
	require.Len(t, cmd.SysProcAttr.UidMappings, 1)
	require.Len(t, cmd.SysProcAttr.GidMappings, 1)
	require.Equal(t, os.Getuid(), cmd.SysProcAttr.UidMappings[0].HostID)
	require.Equal(t, os.Getgid(), cmd.SysProcAttr.GidMappings[0].HostID)
	require.False(t, cmd.SysProcAttr.GidMappingsEnableSetgroups)
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

//go:build linux

package netjail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultMasks are the directories the jail replaces with empty tmpfs
// mounts, so a filesystem unix socket under one of them cannot be reached.
// /var/run is skipped when it is a symlink into /run.
var DefaultMasks = []string{"/run", "/var/run", "/tmp", "/mnt/wslg", "/run/user"}

// Wrap turns cmd into one that runs under the network jail: the same binary
// re-executed through the hidden __jail subcommand, cloned into new user,
// network and mount namespaces. sock, when not empty, is the unix socket of
// the host proxy and port the local port the jail forwards to it. masks are
// the directories the jail hides behind an empty tmpfs; none means
// DefaultMasks. Fields the caller already set on cmd.SysProcAttr, such as
// the process group, are kept.
func Wrap(cmd *exec.Cmd, sock string, port int, masks ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the jail binary: %w", err)
	}
	args := []string{self, "__jail"}
	if sock != "" {
		args = append(args, "--sock", sock, "--port", strconv.Itoa(port))
	}
	for _, mask := range masks {
		args = append(args, "--mask", mask)
	}
	args = append(args, "--")
	args = append(args, cmd.Args...)
	cmd.Path = self
	cmd.Args = args

	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Cloneflags |= syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET | syscall.CLONE_NEWNS
	// The child keeps the caller's uid and gid, so files and tests see the
	// identities they would on the host. The helper still needs three
	// capabilities inside the namespace: net admin to bring loopback up,
	// sys admin to mount the masks, and setpcap to drop the bound set.
	// Main clears all of them before it starts the child.
	uid, gid := os.Getuid(), os.Getgid()
	attr.UidMappings = append(attr.UidMappings, syscall.SysProcIDMap{
		ContainerID: uid, HostID: uid, Size: 1,
	})
	attr.GidMappings = append(attr.GidMappings, syscall.SysProcIDMap{
		ContainerID: gid, HostID: gid, Size: 1,
	})
	attr.GidMappingsEnableSetgroups = false
	attr.AmbientCaps = append(attr.AmbientCaps,
		uintptr(unix.CAP_NET_ADMIN), uintptr(unix.CAP_SYS_ADMIN), uintptr(unix.CAP_SETPCAP))
	cmd.SysProcAttr = attr
	return nil
}

// Preflight checks once, for this process, that the jail can start. Run and
// batch call it before doing any work, so a machine where user namespaces
// are blocked fails with a message that names the way out instead of a bare
// clone error per run.
var Preflight = sync.OnceValue(func() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("network jail unavailable: %w; use --network host", err)
	}
	cmd := exec.CommandContext(context.Background(), self)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := Wrap(cmd, "", 0); err != nil {
		return fmt.Errorf("network jail unavailable: %w; use --network host", err)
	}
	// The jail only sets up and exits; there is no child to run.
	cmd.Args = []string{self, "__jail", "--check"}
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("network jail unavailable (%v: %s); use --network host", err, detail)
		}
		return fmt.Errorf("network jail unavailable (%v); use --network host", err)
	}
	return nil
})

// KillGroup makes cmd a process group leader whose cancellation kills the
// whole group, so a jailed go or the test binaries it starts do not outlive
// the context.
func KillGroup(cmd *exec.Cmd) {
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Setpgid = true
	cmd.SysProcAttr = attr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}

// Main runs the jail side of the hidden __jail subcommand: it makes the
// mount tree private, masks the directories that can hold filesystem
// sockets, brings loopback up, forwards the proxy port when one is given,
// runs the child with the same environment, streams and directory, and
// returns its exit code.
func Main(args []string) int {
	check := len(args) == 1 && args[0] == "--check"
	var sock string
	var port int
	var masks []string
	var argv []string
	if !check {
		var err error
		sock, port, masks, argv, err = parseArgs(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	if len(masks) == 0 {
		masks = DefaultMasks
	}
	masks = unmaskExecutables(masks, argv...)
	if err := privateMounts(); err != nil {
		fmt.Fprintf(os.Stderr, "Netjail: make the mount tree private: %v\n", err)
		return 1
	}
	if err := maskPaths(masks); err != nil {
		fmt.Fprintf(os.Stderr, "Netjail: mask the filesystem sockets: %v\n", err)
		return 1
	}
	if err := loopbackUp(); err != nil {
		fmt.Fprintf(os.Stderr, "Netjail: bring loopback up: %v\n", err)
		return 1
	}
	if sock != "" {
		if err := forward(sock, port); err != nil {
			fmt.Fprintf(os.Stderr, "Netjail: forward the proxy port: %v\n", err)
			return 1
		}
	}
	// Capabilities are per thread, and the child inherits the set of the
	// thread that forks it, so the drop and the exec have to stay on one
	// thread.
	runtime.LockOSThread()
	if err := dropPrivileges(); err != nil {
		fmt.Fprintf(os.Stderr, "Netjail: drop capabilities: %v\n", err)
		return 1
	}
	if check {
		return 0
	}
	return runChild(argv)
}

// dropPrivileges removes everything the helper needed before it starts the
// child: the ambient set, the inheritable and bounding sets, and the right
// to gain privileges through a setuid binary or file capabilities.
func dropPrivileges() error {
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clear the ambient set: %w", err)
	}
	for _, capability := range []uintptr{unix.CAP_SYS_ADMIN, unix.CAP_NET_ADMIN, unix.CAP_SETPCAP} {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, capability, 0, 0, 0); err != nil {
			return fmt.Errorf("drop capability %d from the bounding set: %w", capability, err)
		}
	}
	// Version 3 has two 32-bit words; the second must be zeroed too.
	var data [2]unix.CapUserData
	if err := unix.Capset(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &data[0]); err != nil {
		return fmt.Errorf("clear the capability sets: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	return nil
}

// parseArgs reads the jail's own flags up to the -- that starts the child's
// command line.
func parseArgs(args []string) (sock string, port int, masks []string, argv []string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			argv = args[i+1:]
			if len(argv) == 0 {
				return "", 0, nil, nil, errors.New("netjail: no command after --")
			}
			if sock != "" && port <= 0 {
				return "", 0, nil, nil, errors.New("netjail: --sock needs a positive --port")
			}
			return sock, port, masks, argv, nil
		case "--sock":
			i++
			if i >= len(args) {
				return "", 0, nil, nil, errors.New("netjail: --sock needs a path")
			}
			sock = args[i]
		case "--port":
			i++
			if i >= len(args) {
				return "", 0, nil, nil, errors.New("netjail: --port needs a number")
			}
			port, err = strconv.Atoi(args[i])
			if err != nil {
				return "", 0, nil, nil, fmt.Errorf("netjail: --port %q: %w", args[i], err)
			}
		case "--mask":
			i++
			if i >= len(args) {
				return "", 0, nil, nil, errors.New("netjail: --mask needs a path")
			}
			masks = append(masks, args[i])
		default:
			return "", 0, nil, nil, fmt.Errorf("netjail: unexpected argument %q", args[i])
		}
	}
	return "", 0, nil, nil, errors.New("netjail: no command after --")
}

// unmaskExecutables drops from masks every directory that holds a binary
// the jail still has to run: masking that directory would hide the binary
// from the helper's own exec. The model's binary and the go toolchain live
// outside the default masks, so this only matters for a binary built under
// /tmp, as tests are.
func unmaskExecutables(masks []string, binaries ...string) []string {
	kept := masks[:0]
	for _, mask := range masks {
		hide := true
		for _, binary := range binaries {
			if binary == "" || !filepath.IsAbs(binary) {
				continue
			}
			if binary == mask || strings.HasPrefix(binary, mask+string(filepath.Separator)) {
				hide = false
				break
			}
		}
		if hide {
			kept = append(kept, mask)
		}
	}
	return kept
}

// privateMounts keeps every mount the jail makes out of the host and out of
// other jails.
func privateMounts() error {
	return unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
}

// maskPaths hides each directory behind an empty tmpfs. A path that does
// not exist is skipped, as is /var/run when it is a symlink into /run, which
// is masked already.
func maskPaths(paths []string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}
		if path == "/var/run" && info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			if target == "/run" || strings.HasPrefix(target, "/run/") {
				continue
			}
		}
		if err := unix.Mount("tmpfs", path, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
			return fmt.Errorf("mask %s: %w", path, err)
		}
	}
	return nil
}

// loopbackUp sets IFF_UP on lo. A fresh network namespace has the interface
// down, and without it 127.0.0.1 has no route.
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd) //nolint:errcheck // Only used for the ioctl.
	ifreq, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifreq); err != nil {
		return err
	}
	ifreq.SetUint16(ifreq.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifreq)
}

// forward listens on 127.0.0.1:port inside the jail and copies every
// connection to the host proxy's unix socket.
func forward(sock string, port int) error {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go forwardConn(conn, sock)
		}
	}()
	return nil
}

// forwardConn copies one forwarded connection to the proxy and back.
func forwardConn(conn net.Conn, sock string) {
	defer conn.Close()
	upstream, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sock)
	if err != nil {
		return
	}
	defer upstream.Close()
	closeBoth := closing(conn, upstream)
	var copies sync.WaitGroup
	copies.Add(1)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(upstream, conn)
		closeBoth()
	}()
	_, _ = io.Copy(conn, upstream)
	closeBoth()
	copies.Wait()
}

// runChild runs the command with the jail's own environment, streams and
// directory, forwards SIGINT and SIGTERM, and returns its exit code.
func runChild(argv []string) int {
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	// A helper killed without warning must take its child with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Netjail: start %s: %v\n", argv[0], err)
		return 127
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)

	code := cmd.ProcessState.ExitCode()
	if code >= 0 {
		return code
	}
	// Killed by a signal: report it the way a shell does.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}

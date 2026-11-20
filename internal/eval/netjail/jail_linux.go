//go:build linux

package netjail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

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
	// Its own uid is mapped to root inside the namespace. The kernel gives
	// capabilities in a new user namespace only to a process whose
	// namespace uid is 0, and only such a process may bring loopback up.
	// Files it writes outside are still owned by the host uid, since that
	// is the only id the mapping allows.
	attr.UidMappings = append(attr.UidMappings, syscall.SysProcIDMap{
		ContainerID: 0, HostID: os.Getuid(), Size: 1,
	})
	attr.GidMappings = append(attr.GidMappings, syscall.SysProcIDMap{
		ContainerID: 0, HostID: os.Getgid(), Size: 1,
	})
	attr.GidMappingsEnableSetgroups = false
	cmd.SysProcAttr = attr
	return nil
}

// Main runs the jail side of the hidden __jail subcommand: it makes the
// mount tree private, masks the directories that can hold filesystem
// sockets, brings loopback up, forwards the proxy port when one is given,
// runs the child with the same environment, streams and directory, and
// returns its exit code.
func Main(args []string) int {
	sock, port, masks, argv, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(masks) == 0 {
		masks = DefaultMasks
	}
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
	return runChild(argv)
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
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(upstream, conn)
		closeWrite(upstream)
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(conn, upstream)
		closeWrite(conn)
	}()
	copies.Wait()
}

// runChild runs the command with the jail's own environment, streams and
// directory, forwards SIGINT and SIGTERM, and returns its exit code.
func runChild(argv []string) int {
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
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

package netjail_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
)

// echoServer starts a TCP server on 127.0.0.1 that echoes every byte back
// and returns its host:port.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

// dialProxy opens one connection to the proxy's unix socket.
func dialProxy(t *testing.T, p *netjail.Proxy) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", p.Path())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readResponse reads one response, where method says how to parse it.
func readResponse(t *testing.T, r *bufio.Reader, method string) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(r, &http.Request{Method: method})
	require.NoError(t, err)
	return resp
}

// TestProxyTunnelsConnect checks that an allowlisted CONNECT reaches the
// target and that both directions carry bytes.
func TestProxyTunnelsConnect(t *testing.T) {
	t.Parallel()

	target := echoServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{target})
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.

	conn := dialProxy(t, p)
	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)
	r := bufio.NewReader(conn)
	resp := readResponse(t, r, http.MethodConnect)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	got := make([]byte, 4)
	_, err = io.ReadFull(r, got)
	require.NoError(t, err)
	require.Equal(t, "ping", string(got))

	denied, hosts := p.Denied()
	require.Zero(t, denied)
	require.Empty(t, hosts)
}

// TestProxyDenies checks that a host outside the allowlist and a plain
// request are both refused and counted by host, with no path or body kept.
func TestProxyDenies(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{"allowed.example:443"})
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.

	conn := dialProxy(t, p)
	_, err = conn.Write([]byte("CONNECT denied.example:443 HTTP/1.1\r\nHost: denied.example:443\r\n\r\n"))
	require.NoError(t, err)
	resp := readResponse(t, bufio.NewReader(conn), http.MethodConnect)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	conn = dialProxy(t, p)
	_, err = conn.Write([]byte("CONNECT denied.example:443 HTTP/1.1\r\nHost: denied.example:443\r\n\r\n"))
	require.NoError(t, err)
	resp = readResponse(t, bufio.NewReader(conn), http.MethodConnect)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	conn = dialProxy(t, p)
	_, err = conn.Write([]byte("GET http://denied.example/secret HTTP/1.1\r\nHost: denied.example\r\n\r\n"))
	require.NoError(t, err)
	resp = readResponse(t, bufio.NewReader(conn), http.MethodGet)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	denied, hosts := p.Denied()
	require.Equal(t, 3, denied)
	require.Equal(t, []string{"denied.example", "denied.example:443"}, hosts)
	for _, host := range hosts {
		require.NotContains(t, host, "secret")
	}
}

// TestProxyClosesOnCancel checks that the proxy stops listening and removes
// its socket when the context ends.
func TestProxyClosesOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	sock := filepath.Join(t.TempDir(), "net.sock")
	p, err := netjail.Listen(ctx, sock, nil)
	require.NoError(t, err)
	cancel()

	require.Eventually(t, func() bool {
		conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sock)
		if err != nil {
			return true
		}
		conn.Close()
		return false
	}, 5*time.Second, 10*time.Millisecond, "proxy still accepts after cancel")
	require.NoError(t, p.Close())
}

// TestProxyRejectsAllowEntryWithoutPort checks that a malformed allowlist is
// an error instead of an entry that can never match.
func TestProxyRejectsAllowEntryWithoutPort(t *testing.T) {
	t.Parallel()

	_, err := netjail.Listen(context.Background(), filepath.Join(t.TempDir(), "net.sock"), []string{"allowed.example"})
	require.ErrorContains(t, err, "allowed.example")
}

// TestProxyPathLength checks that the socket path fits the platform's unix
// address, since t.TempDir can be short but a run directory is not.
func TestProxyPathLength(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are tested on Unix only")
	}
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p, err := netjail.Listen(context.Background(), filepath.Join(dir, "net.sock"), nil)
	require.Error(t, err, "a path no unix socket can hold is an error, not a truncated address")
	require.Nil(t, p)
}

// TestEnv checks the proxy variables a jailed child receives.
func TestEnv(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"HTTPS_PROXY=http://127.0.0.1:1234",
		"HTTP_PROXY=http://127.0.0.1:1234",
		"https_proxy=http://127.0.0.1:1234",
		"http_proxy=http://127.0.0.1:1234",
	}, netjail.Env(1234))
}

// TestDefaultMode checks that the jail is the default only where it exists.
func TestDefaultMode(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		require.Equal(t, netjail.ModeJail, netjail.DefaultMode())
		return
	}
	require.Equal(t, netjail.ModeHost, netjail.DefaultMode())
}

// TestPort checks that Port returns a usable TCP port.
func TestPort(t *testing.T) {
	t.Parallel()

	port, err := netjail.Port()
	require.NoError(t, err)
	require.NotZero(t, port)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}

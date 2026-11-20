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

// holdServer starts a TCP server that accepts one connection, reports when
// its side stops reading, and keeps the connection open until the test ends.
// Keeping it open is what makes a half-closed tunnel hang.
func holdServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	closed := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { conn.Close() })
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}()
	return ln.Addr().String(), closed
}

// connect asks the proxy for a tunnel to target and returns the client
// connection after the 200.
func connect(t *testing.T, p *netjail.Proxy, target string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn := dialProxy(t, p)
	_, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)
	r := bufio.NewReader(conn)
	resp := readResponse(t, r, http.MethodConnect)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return conn, r
}

// TestProxyTunnelClosesUpstream checks that the proxy closes the upstream
// side when the client leaves, instead of holding it until the server ends.
func TestProxyTunnelClosesUpstream(t *testing.T) {
	t.Parallel()

	target, closed := holdServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{target})
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.

	conn, _ := connect(t, p, target)
	require.NoError(t, conn.Close())

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the upstream connection stayed open after the client left")
	}
}

// TestProxyTunnelClosesClient checks that the client sees the end when the
// upstream server closes.
func TestProxyTunnelClosesClient(t *testing.T) {
	t.Parallel()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := ln.Addr().String()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{target})
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.

	conn, r := connect(t, p, target)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = r.ReadByte()
	require.Error(t, err, "the client kept reading after the server closed")
}

// TestProxyCloseWithLiveTunnel checks that Close returns while a tunnel is
// still live and that it closes both ends.
func TestProxyCloseWithLiveTunnel(t *testing.T) {
	t.Parallel()

	target, closed := holdServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{target})
	require.NoError(t, err)

	conn, _ := connect(t, p, target)
	done := make(chan error, 1)
	go func() { done <- p.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung with a live tunnel")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close left the upstream connection open")
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err, "the client connection survived Close")
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

// TestProxyAllowlist pins the cases a model could use to slip past the
// allowlist or to make the proxy dial something else.
func TestProxyAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allow   func(target string) string
		request func(target string) string
		want    int
		echo    bool
	}{
		{
			name:    "allowed host",
			allow:   func(target string) string { return target },
			request: func(target string) string { return "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n" },
			want:    http.StatusOK,
			echo:    true,
		},
		{
			name:    "case is ignored",
			allow:   func(target string) string { return strings.ToUpper(target) },
			request: func(target string) string { return "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n" },
			want:    http.StatusOK,
			echo:    true,
		},
		{
			name:  "trailing dot is another host",
			allow: func(target string) string { return localhost(target) },
			request: func(target string) string {
				dotted := strings.Replace(localhost(target), ":", ".:", 1)
				return "CONNECT " + dotted + " HTTP/1.1\r\nHost: " + dotted + "\r\n\r\n"
			},
			want: http.StatusForbidden,
		},
		{
			name:    "missing port",
			allow:   func(target string) string { return target },
			request: func(target string) string { return "CONNECT localhost HTTP/1.1\r\nHost: localhost\r\n\r\n" },
			want:    http.StatusForbidden,
		},
		{
			name:  "a Host header that differs from the CONNECT target",
			allow: func(target string) string { return target },
			request: func(target string) string {
				return "CONNECT " + target + " HTTP/1.1\r\nHost: other.example:443\r\n\r\n"
			},
			want: http.StatusOK,
			echo: true,
		},
		{
			name:  "plain request to an allowed host",
			allow: func(target string) string { return target },
			request: func(target string) string {
				return "GET http://" + target + "/secret HTTP/1.1\r\nHost: " + target + "\r\n\r\n"
			},
			want: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := echoServer(t)
			// localhost and 127.0.0.1 both reach the same server; the
			// request uses the allowlist entry's spelling.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), []string{tt.allow(target)})
			require.NoError(t, err)
			defer p.Close() //nolint:errcheck // Test cleanup.

			conn := dialProxy(t, p)
			_, err = conn.Write([]byte(tt.request(target)))
			require.NoError(t, err)
			r := bufio.NewReader(conn)
			resp := readResponse(t, r, http.MethodConnect)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tt.want, resp.StatusCode)
			if !tt.echo {
				return
			}
			_, err = conn.Write([]byte("ping"))
			require.NoError(t, err)
			got := make([]byte, 4)
			_, err = io.ReadFull(r, got)
			require.NoError(t, err)
			require.Equal(t, "ping", string(got))
		})
	}
}

// localhost is target's port with a name that resolves to it.
func localhost(target string) string {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return target
	}
	return net.JoinHostPort("localhost", port)
}

// TestProxyDenialCapped checks that a model cannot grow the denial log past
// its caps while the total count keeps counting.
func TestProxyDenialCapped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := netjail.Listen(ctx, filepath.Join(t.TempDir(), "net.sock"), nil)
	require.NoError(t, err)
	defer p.Close() //nolint:errcheck // Test cleanup.

	// The long name goes first, while the set still has room.
	long := strings.Repeat("a", 300) + ".example:443"
	deny(t, p, long)
	for i := range 105 {
		deny(t, p, fmt.Sprintf("h%d.example:443", i))
	}

	denied, hosts := p.Denied()
	require.Equal(t, 106, denied, "the total count is not capped")
	require.Len(t, hosts, 100, "the name set is capped")
	for _, host := range hosts {
		require.LessOrEqual(t, len(host), 255, "a denied name is capped")
	}
	require.Contains(t, hosts, long[:255], "the long name is truncated, not dropped")
}

// deny asks the proxy for a host it does not allow and requires a 403.
func deny(t *testing.T, p *netjail.Proxy, target string) {
	t.Helper()
	conn := dialProxy(t, p)
	defer conn.Close()
	_, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)
	resp := readResponse(t, bufio.NewReader(conn), http.MethodConnect)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
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

	for range 10 {
		port, err := netjail.Port()
		require.NoError(t, err)
		require.NotZero(t, port)
		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			// A parallel test may have taken the port; try another.
			continue
		}
		require.NoError(t, ln.Close())
		return
	}
	t.Fatal("Port did not return a bindable port")
}

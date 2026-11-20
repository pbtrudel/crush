// Package netjail keeps a process under test off the network except for
// the one address it needs: the model API. The host side is an HTTP CONNECT
// proxy on a unix socket that tunnels only an allowlist of host:port pairs
// and counts every refusal. The jail side is a hidden subcommand that brings
// loopback up in a fresh network namespace, forwards a local port to the
// proxy, and runs the child.
package netjail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Network modes of a run.
const (
	// ModeJail runs the child in a network namespace with loopback only
	// and forwards its allowlisted proxy port.
	ModeJail = "jail"
	// ModeHost runs the child with the machine's network, as before.
	ModeHost = "host"
)

// DefaultMode is the mode a command uses when none is given: the jail where
// it exists, the host otherwise.
func DefaultMode() string {
	if runtime.GOOS == "linux" {
		return ModeJail
	}
	return ModeHost
}

// Env is the proxy environment of a jailed child, pointing at the jail's
// forwarded port. Go's default transport and git both honor it.
func Env(port int) []string {
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	return []string{
		"HTTPS_PROXY=" + url,
		"HTTP_PROXY=" + url,
		"https_proxy=" + url,
		"http_proxy=" + url,
	}
}

// Port returns a free TCP port. The jail's network namespace holds nothing
// else, so the port only has to be one the jail may bind.
func Port() (int, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find a port for the jail: %w", err)
	}
	defer ln.Close() //nolint:errcheck // Only the port is needed.
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// Proxy is the host side of the jail: an HTTP CONNECT proxy on a unix socket
// that reaches only the allowlisted host:port pairs.
type Proxy struct {
	path  string
	allow map[string]bool
	ln    net.Listener
	stop  func() bool
	once  sync.Once

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]bool
	denied int
	hosts  map[string]bool
	wg     sync.WaitGroup
}

// Listen starts the proxy on path, tunnels only the allowlist of host:port
// pairs, and closes when ctx ends. Every other CONNECT and every plain
// request is refused with 403 and counted.
func Listen(ctx context.Context, path string, allow []string) (*Proxy, error) {
	allowed := make(map[string]bool, len(allow))
	for _, entry := range allow {
		name, err := allowedName(entry)
		if err != nil {
			return nil, err
		}
		allowed[name] = true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the proxy's directory: %w", err)
	}
	// A crashed run can leave the socket behind, and bind refuses a path
	// that exists.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove the old proxy socket: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on the proxy socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close() //nolint:errcheck // Report the chmod instead.
		return nil, fmt.Errorf("tighten the proxy socket: %w", err)
	}

	p := &Proxy{
		path:  path,
		allow: allowed,
		ln:    ln,
		conns: make(map[net.Conn]bool),
		hosts: make(map[string]bool),
	}
	p.wg.Add(1)
	go p.serve()
	p.stop = context.AfterFunc(ctx, func() { _ = p.Close() })
	return p, nil
}

// allowedName is the canonical host:port of one allowlist entry.
func allowedName(entry string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(entry))
	host, port, err := net.SplitHostPort(name)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("netjail: allow entry %q is not host:port", entry)
	}
	return net.JoinHostPort(host, port), nil
}

// Path is the unix socket the proxy listens on.
func (p *Proxy) Path() string { return p.path }

// Denied returns how many connections the proxy refused, and the sorted
// distinct names it refused. A name is a host or host:port, never a path or
// a body.
func (p *Proxy) Denied() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	hosts := make([]string, 0, len(p.hosts))
	for host := range p.hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return p.denied, hosts
}

// Close stops accepting, drops the live tunnels and removes the socket.
func (p *Proxy) Close() error {
	var err error
	p.once.Do(func() {
		if p.stop != nil {
			p.stop()
		}
		err = p.ln.Close()
		p.mu.Lock()
		p.closed = true
		conns := make([]net.Conn, 0, len(p.conns))
		for conn := range p.conns {
			conns = append(conns, conn)
		}
		p.mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		p.wg.Wait()
	})
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// serve accepts proxy connections until the listener closes.
func (p *Proxy) serve() {
	defer p.wg.Done()
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		if !p.start(conn) {
			_ = conn.Close()
			return
		}
		go func() {
			defer p.wg.Done()
			defer p.untrack(conn)
			p.handle(conn)
		}()
	}
}

// start tracks one accepted connection, or reports that the proxy closed
// between the accept and the hand-off.
func (p *Proxy) start(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.conns[conn] = true
	p.wg.Add(1)
	return true
}

// untrack forgets one closed connection.
func (p *Proxy) untrack(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, conn)
}

// handle reads one request and either tunnels a CONNECT to an allowlisted
// target or refuses it.
func (p *Proxy) handle(conn net.Conn) {
	defer conn.Close()
	buffered := bufio.NewReader(conn)
	req, err := http.ReadRequest(buffered)
	if err != nil {
		return
	}
	defer req.Body.Close() //nolint:errcheck // Read-only request.
	target := requestName(req)
	if req.Method != http.MethodConnect || !p.allow[target] {
		p.record(target)
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}

	upstream, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(context.Background(), "tcp", target)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer upstream.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	// Bytes the client sent after the headers belong to the tunnel. When
	// either direction ends, both connections close: a half-close would
	// leave the other copy blocked on a live server and Close would wait
	// for it.
	closeBoth := closing(conn, upstream)
	var copies sync.WaitGroup
	copies.Add(1)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(upstream, buffered)
		closeBoth()
	}()
	_, _ = io.Copy(conn, upstream)
	closeBoth()
	copies.Wait()
}

// closing returns a function that closes both connections once.
func closing(a, b net.Conn) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
}

// Bounds on the denial log. The names are model-controlled, so the set and
// each name are capped; the total count keeps counting.
const (
	maxDeniedNames = 100
	maxDeniedName  = 255
)

// record counts one refusal by the name the request asked for.
func (p *Proxy) record(name string) {
	if name == "" {
		name = "unknown"
	}
	if len(name) > maxDeniedName {
		name = name[:maxDeniedName]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.denied++
	if _, seen := p.hosts[name]; seen || len(p.hosts) < maxDeniedNames {
		p.hosts[name] = true
	}
}

// requestName is the name a request asked for: the authority of a CONNECT,
// or the host of a plain request, without path or body.
func requestName(req *http.Request) string {
	name := req.Host
	if name == "" {
		name = req.URL.Host
	}
	return strings.ToLower(name)
}

package web

import (
	"cmp"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// launchFileTTL bounds how long a launch file no tab used lives before it is
// removed, so a launch nobody followed does not linger until shutdown.
const launchFileTTL = 2 * time.Minute

// launchHTML redirects to the launch URL. The opener only ever sees the
// file's path, so the token stays out of process arguments.
const launchHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0;url=%s"><title>Crush</title></head>
<body><p><a href="%s">Open Crush</a></p></body></html>
`

type launchFile struct{ dir, path string }

func writeLaunchFile(parent, launchURL string) (*launchFile, error) {
	dir, err := os.MkdirTemp(parent, "crush-web-")
	if err != nil {
		return nil, fmt.Errorf("web: launch dir: %w", err)
	}
	path := filepath.Join(dir, "launch.html")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("web: launch file: %w", err)
	}
	esc := html.EscapeString(launchURL)
	_, werr := fmt.Fprintf(f, launchHTML, esc, esc)
	cerr := f.Close()
	if err := cmp.Or(werr, cerr); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("web: launch file: %w", err)
	}
	return &launchFile{dir: dir, path: path}, nil
}

func (l *launchFile) remove() {
	if l != nil {
		_ = os.RemoveAll(l.dir)
	}
}

// launchParent returns the directory launch files are written under. A
// per-user XDG runtime directory is private already; os.TempDir is the
// fallback, and writeLaunchFile still creates a 0700 directory inside it.
func launchParent() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return os.TempDir()
}

// windowsFileURL turns a Windows path, as wslpath -w answers it, into a file
// URL. A UNC path gives file://host/share/rest and a drive path gives
// file:///C:/dir/file; anything else is an error. URL.String escapes each
// path segment, so spaces and percent signs survive the round trip.
func windowsFileURL(p string) (string, error) {
	if rest, ok := strings.CutPrefix(p, `\\`); ok {
		host, tail, ok := strings.Cut(rest, `\`)
		if !ok || host == "" || tail == "" {
			return "", fmt.Errorf("web: not a Windows path: %q", p)
		}
		u := url.URL{Scheme: "file", Host: host, Path: "/" + strings.ReplaceAll(tail, `\`, "/")}
		return u.String(), nil
	}
	if len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		u := url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(p, `\`, "/")}
		return u.String(), nil
	}
	return "", fmt.Errorf("web: not a Windows path: %q", p)
}

func isDriveLetter(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// LaunchFile writes the private redirect page for the browser opener and
// returns its path, creating it only when the gateway has none yet. The page
// carries the token, so the opener gets a token-free file URL instead. The
// file is removed by the first successful auth, by Shutdown, and after
// launchTTL when no tab used it.
func (g *Gateway) LaunchFile() (string, error) {
	g.launchMu.Lock()
	defer g.launchMu.Unlock()
	if g.launch != nil {
		return g.launch.path, nil
	}
	lf, err := writeLaunchFile(launchParent(), g.LaunchURL())
	if err != nil {
		return "", err
	}
	g.launch = lf
	time.AfterFunc(g.launchTTL, func() { g.expireLaunchFile(lf) })
	return lf.path, nil
}

// removeLaunchFile removes the current launch file, if any.
func (g *Gateway) removeLaunchFile() {
	g.launchMu.Lock()
	defer g.launchMu.Unlock()
	g.launch.remove()
	g.launch = nil
}

// expireLaunchFile removes lf unless a later LaunchFile replaced it.
func (g *Gateway) expireLaunchFile(lf *launchFile) {
	g.launchMu.Lock()
	defer g.launchMu.Unlock()
	if g.launch != lf {
		return
	}
	g.launch.remove()
	g.launch = nil
}

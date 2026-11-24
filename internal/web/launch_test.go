package web

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestLaunchFileIsPrivate(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the temp directory private through its ACL")
	}
	l, err := writeLaunchFile(t.TempDir(), "http://127.0.0.1:1234/#t=SECRET&x=<y>")
	require.NoError(t, err)
	t.Cleanup(l.remove)
	fi, err := os.Stat(l.path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(l.path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())
	body, err := os.ReadFile(l.path)
	require.NoError(t, err)
	require.Contains(t, string(body), `content="0;url=http://127.0.0.1:1234/#t=SECRET&amp;x=&lt;y&gt;"`)
	require.NotContains(t, l.path, "SECRET")
}

func TestLaunchFileRemovedAfterAuth(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	g, _ := startTest(t)
	path, err := g.LaunchFile()
	require.NoError(t, err)
	_ = authedClient(t, g)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, fs.ErrNotExist)
	again, err := g.LaunchFile()
	require.NoError(t, err, "a later /web writes a fresh file")
	require.NotEqual(t, path, again)
}

func TestLaunchFileRemovedOnShutdown(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	path, err := g.LaunchFile()
	require.NoError(t, err)
	require.NoError(t, g.Shutdown(context.Background()))
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestLaunchFileAfterShutdown pins that a launch file opened after
// Shutdown writes nothing: a file nobody will remove would outlive the
// process with the token in it.
func TestLaunchFileAfterShutdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	g, err := Start(t.Context(), Options{Workspace: webtest.NewFake()})
	require.NoError(t, err)
	require.NoError(t, g.Shutdown(context.Background()))
	path, err := g.LaunchFile()
	require.Error(t, err)
	require.Empty(t, path)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a closed gateway writes no launch file")
}

// TestLaunchFileReuseExtendsTheTTL pins that a second open resets the TTL
// timer, so a page opened from the same file is not removed seconds after
// it was handed out.
func TestLaunchFileReuseExtendsTheTTL(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	g, err := Start(t.Context(), Options{Workspace: webtest.NewFake(), launchFileTTL: 500 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	path, err := g.LaunchFile()
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	again, err := g.LaunchFile()
	require.NoError(t, err)
	require.Equal(t, path, again)
	// The first timer would have removed the file 500 ms after the first
	// call; the reset one runs 500 ms after the second.
	time.Sleep(300 * time.Millisecond)
	_, err = os.Stat(path)
	require.NoError(t, err, "a reuse resets the TTL")
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err != nil
	}, 2*time.Second, 20*time.Millisecond, "the reset TTL still removes the file")
}

// TestLaunchFileSurvivesAWrongToken pins that only a successful sign-in
// removes the launch file, so a mistyped token leaves the link usable.
func TestLaunchFileSurvivesAWrongToken(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	g, _ := startTest(t)
	path, err := g.LaunchFile()
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth",
		strings.NewReader(`{"token":"nope"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", g.URL())
	rsp := doReq(t, nil, req)
	rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
	_, err = os.Stat(path)
	require.NoError(t, err, "a failed sign-in keeps the launch file")
}

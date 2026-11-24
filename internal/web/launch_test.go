package web

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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

package home

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefault mutates the environment, so it cannot run in parallel with
// the other tests of the package.
func TestDefault(t *testing.T) {
	t.Run("XDG_STATE_HOME", func(t *testing.T) {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		h, err := Default()
		require.NoError(t, err)
		require.Equal(t, filepath.Join(state, "crush-eval"), h.Dir())
	})

	t.Run("default under home", func(t *testing.T) {
		userHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", userHome)
		h, err := Default()
		require.NoError(t, err)
		require.Equal(t, filepath.Join(userHome, ".local", "state", "crush-eval"), h.Dir())
	})

	t.Run("relative XDG_STATE_HOME is ignored", func(t *testing.T) {
		userHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", "relative/state")
		t.Setenv("HOME", userHome)
		h, err := Default()
		require.NoError(t, err)
		require.Equal(t, filepath.Join(userHome, ".local", "state", "crush-eval"), h.Dir())
	})
}

func TestPaths(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	h := At(dir)
	require.Equal(t, dir, h.Dir())
	require.Equal(t, filepath.Join(dir, "routes"), h.RoutesDir())
	require.Equal(t, filepath.Join(dir, "data"), h.DataDir())
	require.Equal(t, filepath.Join(dir, "config", "crush"), h.ConfigDir())
	require.Equal(t, filepath.Join(dir, "skills"), h.SkillsDir())
	require.Equal(t, filepath.Join(dir, "gh"), h.GHDir())
	require.Equal(t, filepath.Join(dir, "claude"), h.ClaudeDir())
	require.Equal(t, filepath.Join(dir, "bin", "abc123", "crush"), h.BinPath("abc123"))
	require.Equal(t, filepath.Join(dir, "batches", "b1"), h.BatchDir("b1"))
}

func TestEnsureCreatesLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	h := At(filepath.Join(root, "state", "crush-eval"))
	require.NoError(t, h.Ensure())

	for _, dir := range []string{
		h.Dir(),
		h.RoutesDir(),
		h.DataDir(),
		h.ConfigDir(),
		h.SkillsDir(),
		h.GHDir(),
		h.ClaudeDir(),
		filepath.Join(h.Dir(), "bin"),
		filepath.Join(h.Dir(), "batches"),
	} {
		info, err := os.Stat(dir)
		require.NoError(t, err, dir)
		require.True(t, info.IsDir(), dir)
		requireMode(t, dir, 0o700)
	}

	// Ensure is idempotent.
	require.NoError(t, h.Ensure())
}

func TestEnsureTightens(t *testing.T) {
	t.Parallel()

	h := At(filepath.Join(t.TempDir(), "crush-eval"))
	require.NoError(t, os.MkdirAll(h.RoutesDir(), 0o755))
	require.NoError(t, os.Chmod(h.Dir(), 0o755))
	require.NoError(t, os.Chmod(h.RoutesDir(), 0o755))

	require.NoError(t, h.Ensure())

	requireMode(t, h.Dir(), 0o700)
	requireMode(t, h.RoutesDir(), 0o700)
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err, path)
	require.Equal(t, want, info.Mode().Perm(), path)
}

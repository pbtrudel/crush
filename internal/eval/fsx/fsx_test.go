package fsx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteFileAtomic checks that a config lands whole, with the mode it was
// given, in a directory that did not exist, and leaves no temporary file.
func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config", "crush.json")
	require.NoError(t, WriteFileAtomic(path, []byte("one\n"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\n", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, WriteFileAtomic(path, []byte("two\n"), 0o644))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "two\n", string(data))
	info, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file is left behind")
}

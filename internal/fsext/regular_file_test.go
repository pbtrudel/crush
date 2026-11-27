package fsext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestReadRegularFileLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	data, err := ReadRegularFileLimit(path, 5)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	_, err = ReadRegularFileLimit(path, 4)
	require.ErrorIs(t, err, ErrFileTooLarge)

	_, err = ReadRegularFileLimit(dir, 1<<20)
	require.ErrorIs(t, err, ErrNotRegularFile)

	_, err = ReadRegularFileLimit(filepath.Join(dir, "missing.md"), 1<<20)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadRegularFSFileLimit(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.md":     {Data: []byte("hello")},
		"big.md":   {Data: []byte(strings.Repeat("x", 10))},
		"dir/b.md": {Data: []byte("b")},
	}
	data, err := ReadRegularFSFileLimit(fsys, "a.md", 5)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	_, err = ReadRegularFSFileLimit(fsys, "big.md", 9)
	require.ErrorIs(t, err, ErrFileTooLarge)

	_, err = ReadRegularFSFileLimit(fsys, "dir", 1<<20)
	require.ErrorIs(t, err, ErrNotRegularFile)
}

//go:build unix

package fsext

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// readWithin fails the test when read does not return within a few seconds,
// which is what a FIFO without a writer or /dev/zero would do unchecked.
func readWithin(t *testing.T, read func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- read() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("read did not return; a special file was opened")
		return nil
	}
}

func TestReadRegularFileLimitRefusesSpecialFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.md")
	require.NoError(t, unix.Mkfifo(fifo, 0o644))
	zero := filepath.Join(dir, "zero.md")
	require.NoError(t, os.Symlink("/dev/zero", zero))

	for _, path := range []string{fifo, zero} {
		err := readWithin(t, func() error {
			_, err := ReadRegularFileLimit(path, 1<<20)
			return err
		})
		require.ErrorIs(t, err, ErrNotRegularFile, path)

		err = readWithin(t, func() error {
			_, err := ReadRegularFSFileLimit(os.DirFS(dir), filepath.Base(path), 1<<20)
			return err
		})
		require.ErrorIs(t, err, ErrNotRegularFile, path)
	}
}

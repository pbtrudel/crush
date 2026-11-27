package fsext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// ErrNotRegularFile is returned for a path that is not a regular file once
// symlinks are followed: a directory, a FIFO, a device or a socket.
var ErrNotRegularFile = errors.New("not a regular file")

// ErrFileTooLarge is returned for a file larger than the caller's limit.
var ErrFileTooLarge = errors.New("file too large")

// ReadRegularFileLimit reads path when it is a regular file of at most limit
// bytes. It stats first, following symlinks, and refuses anything else
// before opening it, so a FIFO cannot block the caller and a link to
// /dev/zero cannot exhaust memory.
func ReadRegularFileLimit(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := checkRegular(path, info, limit); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(path, file, limit)
}

// ReadRegularFSFileLimit is ReadRegularFileLimit for a file system.
func ReadRegularFSFileLimit(fsys fs.FS, name string, limit int64) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, err
	}
	if err := checkRegular(name, info, limit); err != nil {
		return nil, err
	}
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(name, file, limit)
}

func checkRegular(name string, info fs.FileInfo, limit int64) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", name, ErrNotRegularFile)
	}
	if info.Size() > limit {
		return fmt.Errorf("%s: %w: %d bytes, more than %d", name, ErrFileTooLarge, info.Size(), limit)
	}
	return nil
}

// readLimited reads at most limit bytes. The stat above can be stale, so the
// read itself is bounded too.
func readLimited(name string, r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w: more than %d bytes", name, ErrFileTooLarge, limit)
	}
	return data, nil
}

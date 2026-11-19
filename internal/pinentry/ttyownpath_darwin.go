//go:build darwin

package pinentry

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// maxPathLen is the buffer F_GETPATH fills.
const maxPathLen = 1024

// fdTerminalPath returns the device path behind fd. On macOS /dev/fd is
// fdescfs, whose entries are not symlinks, so the path has to be read with
// fcntl(fd, F_GETPATH) instead of readlink.
func fdTerminalPath(fd int) (string, bool) {
	buf := make([]byte, maxPathLen)
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0])))); err != nil {
		return "", false
	}
	path, _, _ := strings.Cut(string(buf), "\x00")
	if path == "" {
		return "", false
	}
	return path, true
}

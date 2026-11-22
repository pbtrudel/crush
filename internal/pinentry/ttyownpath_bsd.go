//go:build freebsd || netbsd || openbsd

package pinentry

import (
	"fmt"
	"os"
)

// fdTerminalPath returns the device path behind fd. These platforms expose
// /dev/fd as a symlink to the descriptor target.
func fdTerminalPath(fd int) (string, bool) {
	path, err := os.Readlink(fmt.Sprintf("/dev/fd/%d", fd))
	if err != nil || path == "" {
		return "", false
	}
	return path, true
}

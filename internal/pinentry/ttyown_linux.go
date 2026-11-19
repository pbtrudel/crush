//go:build linux

package pinentry

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// terminalRef identifies the terminal Crush runs on by device and rdev, so
// aliases (/dev/tty, /dev/pts/N, /proc/self/fd/0) cannot be confused.
type terminalRef struct {
	dev  uint64
	rdev uint64
}

// ownTerminalRef identifies the terminal Crush runs on. It tries the
// standard streams in turn, because only one of them needs to be a
// terminal. A pinentry that opens /dev/tty instead of the device the agent
// named would not match, but an agent-spawned pinentry has no controlling
// terminal and opens the pts by name, so the device is what to compare.
func ownTerminalRef() (terminalRef, bool) {
	// /dev/null is a character device too, so the device type of a
	// descriptor is not enough: only the terminal ioctls prove it.
	for _, fd := range []int{0, 1, 2} {
		if _, err := unix.IoctlGetTermios(fd, unix.TCGETS); err != nil {
			continue
		}
		target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
		if err != nil {
			continue
		}
		if ref, ok := terminalRefAt(target); ok {
			return ref, true
		}
	}
	return terminalRef{}, false
}

// terminalRefAt describes the terminal device at path.
func terminalRefAt(path string) (terminalRef, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return terminalRef{}, false
	}
	return terminalRef{dev: uint64(st.Dev), rdev: uint64(st.Rdev)}, true
}

// ownsTerminal reports whether pid holds Crush's terminal open. gpg-agent
// is a daemon with no controlling terminal, so its pinentry child shows
// "?" in ps while still holding the terminal device open (pinentry opens
// the ttyname the agent passed); the open descriptors are the only
// reliable attribution. Unreadable descriptors (another user, restricted
// /proc) mean "not ours".
func ownsTerminal(pid int, ref terminalRef) bool {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	ents, err := os.ReadDir(dir)
	if err != nil {
		// A pid that exited between the snapshot and this check is not a
		// failure worth logging.
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("Pinentry watcher: cannot read fds", "pid", pid, "error", err)
		}
		return false
	}
	for _, e := range ents {
		var st syscall.Stat_t
		if err := syscall.Stat(filepath.Join(dir, e.Name()), &st); err != nil {
			continue
		}
		if uint64(st.Dev) == ref.dev && uint64(st.Rdev) == ref.rdev {
			return true
		}
	}
	return false
}

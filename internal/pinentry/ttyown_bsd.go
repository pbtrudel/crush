//go:build darwin || freebsd || netbsd || openbsd

package pinentry

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// logNoLsofOnce reports the degraded fallback once per process instead of
// once per poll.
var logNoLsofOnce sync.Once

// terminalRef identifies the terminal Crush runs on by path: these
// platforms expose no descriptor table to compare device numbers with, so
// the check resolves the terminal name and asks lsof for the descriptors
// of a candidate.
type terminalRef struct {
	path string
}

// ownTerminalRef identifies the terminal Crush runs on from stdin. A stdin
// that is not a terminal (a pipe, /dev/null) leaves the terminal
// unidentified.
func ownTerminalRef() (terminalRef, bool) {
	if _, err := unix.IoctlGetTermios(0, unix.TIOCGETA); err != nil {
		return terminalRef{}, false
	}
	target, err := os.Readlink("/dev/fd/0")
	if err != nil {
		return terminalRef{}, false
	}
	return terminalRefAt(target)
}

// terminalRefAt describes the terminal device at path.
func terminalRefAt(path string) (terminalRef, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return terminalRef{}, false
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return terminalRef{path: path}, true
}

// ownsTerminal reports whether pid holds Crush's terminal open. gpg-agent
// is a daemon with no controlling terminal, so its pinentry child shows
// "?" in ps while still holding the terminal device open; the open
// descriptors are the only reliable attribution. Without lsof the
// handover keeps the previous behaviour (every candidate is ours) rather
// than losing it entirely.
func ownsTerminal(pid int, ref terminalRef) bool {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "0-99", "-Fn").Output()
	if err != nil {
		logNoLsofOnce.Do(func() {
			slog.Debug("Pinentry watcher: lsof unavailable; not filtering by terminal", "error", err)
		})
		return true
	}
	for line := range strings.Lines(string(out)) {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "n")
		if ok && name == ref.path {
			return true
		}
	}
	return false
}

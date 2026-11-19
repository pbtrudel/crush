//go:build darwin || freebsd || netbsd || openbsd

package pinentry

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// lsofTimeout bounds one lsof run, so a hung lsof cannot stall the watcher
// for longer than a few polls.
const lsofTimeout = 2 * time.Second

// lookLsof returns the path of lsof, resolved once per process. It is
// empty when lsof is not installed, which keeps the previous name-only
// behaviour instead of losing the handover entirely.
var lookLsof = sync.OnceValue(func() string {
	path, err := exec.LookPath("lsof")
	if err != nil {
		slog.Debug("Pinentry watcher: lsof not found; not filtering by terminal")
		return ""
	}
	return path
})

// logLsofFailureOnce reports a broken lsof once per process rather than
// once per candidate.
var logLsofFailureOnce sync.Once

// terminalRef identifies the terminal Crush runs on by path: these
// platforms expose no descriptor table to compare device numbers with, so
// the check resolves the terminal name and asks lsof for the descriptors
// of a candidate.
type terminalRef struct {
	path string
}

// ownTerminalRef identifies the terminal Crush runs on. It tries the
// standard streams in turn, because only one of them needs to be a
// terminal. A pinentry that opens /dev/tty instead of the device the agent
// named would not match, but an agent-spawned pinentry has no controlling
// terminal and opens the pts by name, so the device is what to compare.
func ownTerminalRef() (terminalRef, bool) {
	for _, fd := range []int{0, 1, 2} {
		if _, err := unix.IoctlGetTermios(fd, unix.TIOCGETA); err != nil {
			continue
		}
		path, ok := fdTerminalPath(fd)
		if !ok {
			continue
		}
		if ref, ok := terminalRefAt(path); ok {
			return ref, true
		}
	}
	return terminalRef{}, false
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
// descriptors are the only reliable attribution. A missing or broken lsof
// keeps the previous behaviour (every candidate is ours) rather than
// losing the handover entirely.
func ownsTerminal(pid int, ref terminalRef) bool {
	path := lookLsof()
	if path == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-a", "-p", strconv.Itoa(pid), "-d", "0-99", "-Fn").Output()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		logLsofFailureOnce.Do(func() {
			slog.Debug("Pinentry watcher: cannot run lsof; not filtering by terminal", "error", err)
		})
		return true
	}
	// lsof exits non-zero when its filter selects nothing, so that is a
	// "no match" rather than a failure.
	return lsofMentions(out, ref.path)
}

package pinentry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
)

// terminalPinentryNames are pinentry program basenames that render
// directly on the terminal and therefore need a terminal handover. GUI
// flavors (pinentry-mac, pinentry-qt, pinentry-gnome3, pinentry-gtk, ...)
// are deliberately excluded: they do not touch the terminal. The plain
// "pinentry" dispatcher is included because it is commonly a curses build
// (e.g. Homebrew) or a distribution alternative symlink.
var terminalPinentryNames = map[string]struct{}{
	"pinentry":        {},
	"pinentry-curses": {},
	"pinentry-tty":    {},
}

// gpgNames are the basenames of gpg binaries, watched after a passphrase
// dialog closes to detect a security key touch wait.
var gpgNames = map[string]struct{}{
	"gpg":  {},
	"gpg2": {},
}

// Proc is a process table snapshot entry.
type Proc struct {
	// PID is the process ID.
	PID int
	// PPID is the ID of the parent process, used to tell this Crush's
	// own gpg processes from the gpg of another program.
	PPID int
	// Name is the basename of the process executable.
	Name string
	// Path is the full path of the process executable when the process
	// table provides one (e.g. ps comm on macOS), empty otherwise.
	Path string
}

// Lister returns a snapshot of the process table.
type Lister func(ctx context.Context) ([]Proc, error)

// psLister lists processes via ps(1). On Windows it returns nothing:
// pinentry flavors there are GUI programs that never need the terminal.
func psLister(ctx context.Context) ([]Proc, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}
	return parsePS(out), nil
}

// parsePS parses the output of `ps -eo pid=,ppid=,comm=`. The comm column
// is a full path on some platforms (macOS) and a bare name on others
// (Linux), and it may contain spaces, so everything after the two numeric
// fields is treated as the command and reduced to its basename.
func parsePS(out []byte) []Proc {
	var procs []Proc
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		comm := strings.Join(fields[2:], " ")
		p := Proc{PID: pid, PPID: ppid, Name: path.Base(comm)}
		if strings.Contains(comm, "/") {
			p.Path = comm
		}
		procs = append(procs, p)
	}
	return procs
}

// maxAncestryDepth bounds the parent chain walk, so a malformed snapshot
// (a truncated table, a parent cycle) cannot spin.
const maxAncestryDepth = 64

// descendsFrom reports whether pid has root in its parent chain.
func descendsFrom(pid, root int, parents map[int]int) bool {
	for range maxAncestryDepth {
		ppid, ok := parents[pid]
		if !ok || ppid <= 0 {
			return false
		}
		if ppid == root {
			return true
		}
		pid = ppid
	}
	return false
}

// classify inspects a process table snapshot for pinentry and gpg
// processes. handover reports that a terminal pinentry dialog is present
// and needs exclusive control of the terminal. ctrlL reports that the
// dialog is an ncurses flavor that redraws on Ctrl-L. gpg reports that a
// gpg process started by this Crush is running.
//
// owns reports whether a candidate process holds the terminal Crush runs
// on; a nil owns accepts every candidate, because the terminal cannot be
// identified on this platform or in this environment and the handover
// degrades to a process-name match.
func classify(procs []Proc, owns func(pid int) bool) (handover, ctrlL, gpg bool) {
	// Only a gpg this Crush started can be waiting for the security key
	// it is about to ask for; the gpg of another program or of another
	// Crush instance cannot.
	parents := make(map[int]int, len(procs))
	for i := range procs {
		parents[procs[i].PID] = procs[i].PPID
	}
	self := os.Getpid()
	for i := range procs {
		p := procs[i]
		if _, ok := gpgNames[p.Name]; ok && descendsFrom(p.PID, self, parents) {
			gpg = true
		}
		if _, ok := terminalPinentryNames[p.Name]; !ok {
			continue
		}
		if owns != nil && !owns(p.PID) {
			// The dialog is not drawing on Crush's terminal: it belongs
			// to another terminal, another Crush instance or another
			// user, so this instance must keep its terminal.
			continue
		}
		switch resolveFlavor(p) {
		case flavorCurses:
			handover, ctrlL = true, true
		case flavorTTY:
			handover = true
		case flavorGUI:
			// GUI pinentry flavors draw their own window and never
			// touch the terminal.
		}
	}
	return handover, ctrlL, gpg
}

//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package pinentry

// terminalRef identifies the terminal Crush runs on. This platform has no
// descriptor table to compare against, and the process lister is empty on
// Windows anyway, so only the presence of the type matters here.
type terminalRef struct{}

// ownTerminalRef reports that the terminal cannot be identified, which
// keeps the previous name-only behaviour.
func ownTerminalRef() (terminalRef, bool) {
	return terminalRef{}, false
}

// terminalRefAt reports that the terminal cannot be identified.
func terminalRefAt(string) (terminalRef, bool) {
	return terminalRef{}, false
}

// ownsTerminal accepts every candidate: without an identified terminal
// the handover degrades to the process-name match.
func ownsTerminal(int, terminalRef) bool {
	return true
}

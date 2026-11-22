package tools

import (
	"os"
	"strconv"
	"sync"

	"charm.land/fantasy"
)

// unreadEditNote tells the caller that the edit was applied to a file the
// read guard would have refused, so the rest of the file may not be what it
// last saw.
const unreadEditNote = "Note: the file had not been read in this session, or changed since it was read. The edit was applied because old_string matched exactly once; the rest of the file may be different from the text you last saw."

// unreadEditHint explains, on a refusal the guard kept, what the option
// would have allowed. Reading the file stays the recommendation: only a
// match that appears exactly once is edited this way.
const unreadEditHint = "Reading the file first is still recommended: with edit_without_read on, every edit in the batch must match exactly once."

// readGuard is what the file tracker says about a file an edit opened. The
// zero value means the file was read in this session and has not changed
// since; any other value is the refusal the guard gives.
type readGuard struct {
	kind ErrorKind
	text string
}

// refused reports whether the guard refuses the file.
func (g readGuard) refused() bool { return g.kind != "" }

// refusal is the guard's refusal of an edit. withOption adds the sentence
// that explains why the edit did not apply anyway.
func (g readGuard) refusal(withOption bool) fantasy.ToolResponse {
	text := g.text
	if withOption {
		text += "\n\n" + unreadEditHint
	}
	return withErrorKind(fantasy.NewTextErrorResponse(text), g.kind)
}

// editWithoutReadEnv reads the environment override once: it cannot change
// mid-process, and every edit asks it.
var editWithoutReadEnv = sync.OnceValue(readEditWithoutReadEnv)

// readEditWithoutReadEnv parses CRUSH_EDIT_WITHOUT_READ, which turns the
// option on for one run without touching the config file.
func readEditWithoutReadEnv() bool {
	enabled, _ := strconv.ParseBool(os.Getenv("CRUSH_EDIT_WITHOUT_READ"))
	return enabled
}

// editWithoutReadEnabled reports whether an edit may apply to a file not
// read first. The config option is the normal switch; the environment
// variable lets a harness turn it on for one run.
func editWithoutReadEnabled(option bool) bool {
	return option || editWithoutReadEnv()
}

// mayEditUnread reports whether an edit takes the path that applies to a
// file the guard refuses, and whether the guard refuses the file at all.
// The path needs the option and a match that is exact, single and not
// replace_all: notExact says the match failed, only matched through the
// whitespace fallback, or could not prove itself unique.
func (edit editContext) mayEditUnread(guard readGuard, replaceAll, notExact bool) (degraded, refused bool) {
	if !guard.refused() {
		return false, false
	}
	if !editWithoutReadEnabled(edit.withoutRead) || replaceAll || notExact {
		return false, true
	}
	return true, false
}

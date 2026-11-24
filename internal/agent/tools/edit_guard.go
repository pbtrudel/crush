package tools

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/fsext"
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

// refusalWindow is how many lines a refusal shows: the window view reads by
// default, so a refusal never floods the context more than a view would.
const refusalWindow = DefaultReadLimit

// refusalContent is the window of the current file a refusal shows.
type refusalContent struct {
	// window is the numbered text of the window.
	window string
	// start and end are the 1-based first and last line shown, and total is
	// the file's line count.
	start, end, total int
	// wholeFile reports whether the window is the entire file.
	wholeFile bool
}

// refuseWithContent refuses the edit and hands back the current file, so
// the retry does not need a view first: the model sees the text the guard
// wants it to see, and that read is recorded. withOption adds the sentence
// that explains why the edit did not apply anyway. Files view itself
// refuses (binary, image, over its size limit) keep the plain refusal.
func (edit editContext) refuseWithContent(guard readGuard, filePath, oldString string, withOption bool) fantasy.ToolResponse {
	content, ok := edit.currentContent(filePath, oldString)
	if !ok {
		return guard.refusal(withOption)
	}
	edit.recordShownRead(filePath)
	text := guard.text + "\n\n" + fmt.Sprintf(
		"Showing lines %d-%d of %d, now marked as read; retry the edit against it:\n",
		content.start, content.end, content.total,
	) + content.window
	if withOption {
		text += "\n\n" + unreadEditHint
	}
	return withErrorKind(fantasy.NewTextErrorResponse(text), guard.kind)
}

// refuseOverwrite refuses a write. A write replaces every line, so the file
// is shown and the read recorded only when the window is the whole file: a
// partial window would let the overwrite through on lines the model never
// saw, so it only names what is left to read.
func (edit editContext) refuseOverwrite(guard readGuard, filePath string) fantasy.ToolResponse {
	content, ok := edit.currentContent(filePath, "")
	if !ok {
		return guard.refusal(false)
	}
	if !content.wholeFile {
		text := guard.text + "\n\n" + fmt.Sprintf(
			"this shows lines %d-%d of %d; view the rest, or use edit for a targeted change:\n",
			content.start, content.end, content.total,
		) + content.window
		return withErrorKind(fantasy.NewTextErrorResponse(text), guard.kind)
	}
	edit.recordShownRead(filePath)
	text := guard.text + "\n\n" + fmt.Sprintf(
		"Showing lines %d-%d of %d, now marked as read; retry the write against it:\n",
		content.start, content.end, content.total,
	) + content.window
	return withErrorKind(fantasy.NewTextErrorResponse(text), guard.kind)
}

// recordShownRead records the read a refusal just showed, so the retry passes
// the guard. It reports nothing when the session is unknown.
func (edit editContext) recordShownRead(filePath string) {
	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" || edit.filetracker == nil {
		return
	}
	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)
}

// currentContent renders the numbered window of the file around the first
// line of oldString, and reports false when view would refuse the file for
// its size or content.
func (edit editContext) currentContent(filePath, oldString string) (refusalContent, bool) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() || info.Size() > MaxViewSize {
		return refusalContent{}, false
	}
	raw, err := os.ReadFile(filePath)
	if err != nil || !utf8.Valid(raw) {
		return refusalContent{}, false
	}

	content, _ := fsext.ToUnixLineEndings(string(raw))
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	total := len(lines)
	match := 0
	if oldString != "" {
		if index := strings.Index(content, oldString); index >= 0 {
			match = strings.Count(content[:index], "\n")
		}
	}
	start := min(max(0, match-refusalWindow/2), max(0, total-refusalWindow))
	end := min(total, start+refusalWindow)
	window := slices.Clone(lines[start:end])
	for i, line := range window {
		if len(line) > MaxLineLength {
			// Truncate at a rune boundary, the way view does.
			window[i] = strings.ToValidUTF8(line[:MaxLineLength], "") + "..."
		}
	}

	return refusalContent{
		window:    addLineNumbers(strings.Join(window, "\n"), start+1),
		start:     start + 1,
		end:       end,
		total:     total,
		wholeFile: start == 0 && end == total,
	}, true
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

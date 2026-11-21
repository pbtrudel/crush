package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/charmbracelet/crush/internal/eval/task"
)

// marker is one string design 3.7 scans tool-call input for.
type marker struct {
	text string
	// command is the pattern of a marker that names a command. It matches
	// only a whole command, so "gh pr" no longer matches inside "through
	// prefix". A nil command makes the marker a substring.
	command *regexp.Regexp
	// own is a path that is cut out of the text before the marker is
	// matched. The run's own directory sits under the eval home's batches,
	// and the child names it all the time, so it is not contact with
	// another batch.
	own string
}

// commandPattern compiles the pattern of a command marker: the words of the
// marker joined by any run of whitespace, at the start of the text or after
// a separator, and followed by the end of the text or a separator. "/" is a
// separator, so "/usr/bin/gh pr" matches, while "-" and "_" join the words
// of a name, so "gh-pr" and "gh_pr" do not match.
func commandPattern(text string) *regexp.Regexp {
	words := strings.Fields(text)
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	return regexp.MustCompile(`(^|[^a-z0-9_.-])` + strings.Join(words, `\s+`) + `($|[^a-z0-9_-])`)
}

// contaminationMarkers are the strings design 3.7 scans tool calls for: the
// task's merge commit, the fork's pull request and repository URLs, gh
// commands, git's object reader, the suite path, and the host paths a jailed
// run hides: the repository, the checkout the tasks come from, the worktrees
// beside it and the eval home's batches.
func contaminationMarkers(tk *task.Task, paths Paths) []marker {
	markers := []marker{
		{text: "pbtrudel/crush/pull"},
		{text: "github.com/pbtrudel/crush"},
		{text: "gh pr", command: commandPattern("gh pr")},
		{text: "gh api", command: commandPattern("gh api")},
		{text: "git cat-file", command: commandPattern("git cat-file")},
	}
	if tk.Source.Merge != "" {
		markers = append(markers, marker{text: tk.Source.Merge})
	}
	seen := make(map[string]bool, len(markers))
	for _, dir := range []string{paths.Suite, paths.Repo, paths.Checkout, paths.Worktrees} {
		markers = appendPathMarker(markers, seen, dir, "")
	}
	markers = appendPathMarker(markers, seen, paths.Batches, absPath(paths.Own))
	for i, m := range markers {
		markers[i].text = strings.ToLower(m.text)
		markers[i].own = strings.ToLower(m.own)
	}
	return markers
}

// appendPathMarker adds dir, as an absolute path, as a marker once. It skips
// an empty path and one that another marker already names.
func appendPathMarker(markers []marker, seen map[string]bool, dir, own string) []marker {
	abs := absPath(dir)
	if abs == "" || seen[abs] {
		return markers
	}
	seen[abs] = true
	return append(markers, marker{text: abs, own: own})
}

// absPath is dir as an absolute path, or empty when it has none to give.
func absPath(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return abs
}

// Matches opens the session database at path and counts the tool calls whose
// input names one of the task's contamination markers. It keeps no text.
func Matches(path string, tk *task.Task, paths Paths) (int, error) {
	return scanDatabase(path, contaminationMarkers(tk, paths))
}

// scanDatabase opens the run's own session database read-only and counts
// the tool calls whose input names one of the markers. It keeps no text.
func scanDatabase(path string, markers []marker) (int, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat session database: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_txlock=immediate")
	if err != nil {
		return 0, fmt.Errorf("open session database: %w", err)
	}
	defer db.Close() //nolint:errcheck // Read-only.
	rows, err := db.QueryContext(context.Background(), "SELECT parts FROM messages")
	if err != nil {
		return 0, fmt.Errorf("query session messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // Read-only.

	count := 0
	for rows.Next() {
		var parts string
		if err := rows.Scan(&parts); err != nil {
			return 0, err
		}
		count += contaminatedCalls(parts, markers)
	}
	return count, rows.Err()
}

// contaminatedCalls counts the tool calls of one message whose input names a
// marker: design 3.7 counts tool calls, not messages.
func contaminatedCalls(parts string, markers []marker) int {
	var list []struct {
		Type string `json:"type"`
		Data struct {
			Input string `json:"input"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(parts), &list) != nil {
		return 0
	}
	count := 0
	for _, part := range list {
		if part.Type != "tool_call" || part.Data.Input == "" {
			continue
		}
		if namesMarker(part.Data.Input, markers) {
			count++
		}
	}
	return count
}

// namesMarker reports whether one tool call's input names a marker.
func namesMarker(input string, markers []marker) bool {
	texts := inputTexts(input)
	for _, m := range markers {
		if m.text == "" {
			continue
		}
		for _, text := range texts {
			if m.matches(text) {
				return true
			}
		}
	}
	return false
}

// inputTexts is the text a marker is matched against. A tool call's input is
// the JSON object of its arguments, so its string values are decoded and
// matched one by one: an escaped newline or the closing quote of the object
// then reads as the separator it stands for. An input that is not a JSON
// object is matched as it is.
func inputTexts(input string) []string {
	var object map[string]any
	if json.Unmarshal([]byte(input), &object) != nil {
		return []string{strings.ToLower(input)}
	}
	var texts []string
	collectStrings(object, &texts)
	return texts
}

// collectStrings appends every string value of value to texts.
func collectStrings(value any, texts *[]string) {
	switch v := value.(type) {
	case string:
		*texts = append(*texts, strings.ToLower(v))
	case map[string]any:
		for _, child := range v {
			collectStrings(child, texts)
		}
	case []any:
		for _, child := range v {
			collectStrings(child, texts)
		}
	}
}

// matches reports whether text names the marker. A command marker must name a
// whole command; every other marker matches anywhere. The run's own directory
// is cut out first, so a tool call that names its own checkout matches
// nothing.
func (m marker) matches(text string) bool {
	if m.own != "" {
		text = strings.ReplaceAll(text, m.own, "")
	}
	if m.command == nil {
		return strings.Contains(text, m.text)
	}
	return m.command.MatchString(text)
}

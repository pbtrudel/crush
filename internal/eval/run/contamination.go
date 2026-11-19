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
	"strings"

	_ "modernc.org/sqlite"

	"github.com/charmbracelet/crush/internal/eval/task"
)

// marker is one string design 3.7 scans tool-call input for.
type marker struct {
	text string
	// command is set on the markers that name a command. They match only
	// where a command starts, so "gh pr" no longer matches inside
	// "through prefix".
	command bool
}

// contaminationMarkers are the strings design 3.7 scans tool calls for: the
// task's merge commit, the fork's pull request and repository URLs, gh
// commands, the suite path, the repository path and git's object reader.
func contaminationMarkers(tk *task.Task, suiteDir, repoDir string) []marker {
	markers := []marker{
		{text: "pbtrudel/crush/pull"},
		{text: "github.com/pbtrudel/crush"},
		{text: "gh pr", command: true},
		{text: "gh api", command: true},
		{text: "git cat-file", command: true},
	}
	if tk.Source.Merge != "" {
		markers = append(markers, marker{text: tk.Source.Merge})
	}
	for _, dir := range []string{suiteDir, repoDir} {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			markers = append(markers, marker{text: abs})
		}
	}
	for i, m := range markers {
		markers[i].text = strings.ToLower(m.text)
	}
	return markers
}

// Matches opens the session database at path and counts the tool calls whose
// input names one of the task's contamination markers. It keeps no text.
func Matches(path string, tk *task.Task, suiteDir, repoDir string) (int, error) {
	return scanDatabase(path, contaminationMarkers(tk, suiteDir, repoDir))
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
		input := strings.ToLower(part.Data.Input)
		for _, m := range markers {
			if m.text == "" {
				continue
			}
			if m.matches(input) {
				count++
				break
			}
		}
	}
	return count
}

// matches reports whether input names the marker. A command marker must
// start a command: at the start of the input or after a separator, and
// followed by whitespace or the end. Every other marker matches anywhere.
func (m marker) matches(input string) bool {
	if !m.command {
		return strings.Contains(input, m.text)
	}
	for start := 0; ; {
		i := strings.Index(input[start:], m.text)
		if i < 0 {
			return false
		}
		at := start + i
		if commandStart(input, at) && commandEnd(input, at+len(m.text)) {
			return true
		}
		start = at + 1
	}
}

// commandStart reports whether a command may start at index at: the start of
// the input, or after whitespace, a separator or a quote. "(" also covers
// "$(", and the quotes cover the JSON object a tool call's input holds.
func commandStart(input string, at int) bool {
	if at == 0 {
		return true
	}
	switch input[at-1] {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(', '`', '"', '\'':
		return true
	}
	return false
}

// commandEnd reports whether a command name ends at index end: the end of the
// input, or before whitespace.
func commandEnd(input string, end int) bool {
	if end == len(input) {
		return true
	}
	switch input[end] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

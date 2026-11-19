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

// contaminationMarkers are the strings design 3.7 scans tool calls for: the
// task's merge commit, the fork's pull request and repository URLs, gh
// commands, the suite path, the repository path and git's object reader.
func contaminationMarkers(tk *task.Task, suiteDir, repoDir string) []string {
	markers := []string{
		tk.Source.Merge,
		"pbtrudel/crush/pull",
		"github.com/pbtrudel/crush",
		"gh pr",
		"gh api",
		"git cat-file",
	}
	for _, dir := range []string{suiteDir, repoDir} {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			markers = append(markers, abs)
		}
	}
	for i, marker := range markers {
		markers[i] = strings.ToLower(marker)
	}
	return markers
}

// scanDatabase opens the run's own session database read-only and counts
// the tool calls whose input names one of the markers. It keeps no text.
func scanDatabase(path string, markers []string) (int, error) {
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
func contaminatedCalls(parts string, markers []string) int {
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
		for _, marker := range markers {
			if marker != "" && strings.Contains(input, marker) {
				count++
				break
			}
		}
	}
	return count
}

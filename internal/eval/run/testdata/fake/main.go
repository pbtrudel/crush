// Command fake stands in for the crush binary in the runner's tests. It
// records its arguments and prompt, writes or leaves the checkout alone,
// can create a session database with a tool call, then prints a canned
// object and exits with a chosen code.
package main

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	args := os.Args[1:]
	var cwd, dataDir string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--cwd":
			cwd = args[i+1]
		case "--data-dir":
			dataDir = args[i+1]
		}
	}

	if path := os.Getenv("FAKE_ARGV"); path != "" {
		write(path, strings.Join(args, "\n"))
	}
	if path := os.Getenv("FAKE_PROMPT"); path != "" {
		data, _ := io.ReadAll(os.Stdin)
		write(path, string(data))
	}
	if rel := os.Getenv("FAKE_WRITE"); rel != "" {
		path := filepath.Join(cwd, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			write(path, os.Getenv("FAKE_CONTENT"))
		}
	}
	if schema := os.Getenv("FAKE_DB_SQL"); schema != "" {
		createDatabase(dataDir, schema, os.Getenv("FAKE_DB_PART"))
	}
	if ms, _ := strconv.Atoi(os.Getenv("FAKE_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	os.Stdout.WriteString(os.Getenv("FAKE_OUTPUT"))
	code, _ := strconv.Atoi(os.Getenv("FAKE_EXIT"))
	os.Exit(code)
}

// write writes content to path, ignoring failures: the test asserts the
// files it needs exist.
func write(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0o600)
}

// createDatabase creates the run's session database with the given schema
// and one assistant message carrying part as its parts JSON.
func createDatabase(dataDir, schema, part string) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "crush.db"))
	if err != nil {
		return
	}
	defer db.Close() //nolint:errcheck // Read-only test fixture.
	if _, err := db.Exec(schema); err != nil {
		return
	}
	if part == "" {
		return
	}
	_, _ = db.Exec(
		`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m1', 's1', 'assistant', ?, 1, 1)`,
		part,
	)
}

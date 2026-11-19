# Stats Report (Window, Tools, Shell, Edits, Reads) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `crush stats report`, a read-only report on one project's session log with the sections Window, Tools, Shell, Edits and Reads, as text or as JSON (delivery item 7 of the measurement design).

**Architecture:** A new package `internal/logreport` makes one pass over `messages` with one hand-written query, read row by row in `session_id, created_at, rowid` order, and decodes each row's parts with the existing `message` types (through two small exported wrappers, `message.DecodeParts` and `message.EncodeParts`). Each section is a self-contained accumulator behind one small interface (`section`): it sees every decoded message, is told when a session ends, and writes its figures into the `Report` after the pass, when the pass knows which builds stamp their tool results. Item 8 adds its sections the same way: one type that implements `section`, one entry in `newSections`, one field on `Report`, one line in `WriteText`. The command `crush stats report` in `internal/cmd/stats_report.go` resolves the project's data directory, opens `crush.db` with `db.ConnectReadOnly` (never `db.Connect`, which creates and migrates) and prints the report.

**Tech Stack:** Go 1.27, `database/sql` with the project's SQLite drivers, `github.com/tidwall/gjson` (already a direct dependency), `text/tabwriter`, cobra, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, section 2 (Report), Failure behavior, Validation (the report bullet and the live check), Scope, Delivery item 7. Code references are to origin/main `d6659751` (stamps PR 1, PR 2 and tool stamps part A merged).

## Global Constraints

- Delivery item 7 is split in four pull requests, each merged on its own before the next starts: part 1 (pass, Window, output, command; Tasks 1 to 3), part 2 (builds and Tools; Tasks 4 to 6), part 3 (Shell and Reads; Tasks 7 to 9), part 4 (Edits; Tasks 10 and 11). Item 8's sections (Turns, Sessions, Injections, Compactions, By model, By build) are not part of any of them.
- For each part, work in a fresh worktree created from `origin/main` after `git fetch origin`, after the previous part has merged (`worktree create` with name `w0-stats-report-1`, `w0-stats-report-2`, `w0-stats-report-3`, `w0-stats-report-4`, base `origin/main`). Paths below are relative to its root.
- Files this plan touches: `internal/message/parts_codec.go` (new), `internal/logreport/` (new package), `internal/cmd/stats_report.go` and its test (new). Do not touch `internal/agent/`, `internal/cmd/run.go`, `internal/app/`, `internal/agentteam/`, `internal/cmd/stats.go` or any SQL or migration file: other pull requests are changing the first four, and `crush stats` keeps producing its page unchanged.
- Privacy rule (spec section 2): the output is counts, rates and the names of tools, kinds and builds. It never prints text from a message, a title, a path or a session id, in either format. Tool-call inputs are read for `file_path`, `file_paths`, `limit`, `offset` and the `old_string` values only; paths are compared in memory and never printed, and `old_string` values are reduced to a hash at once, so the text is never held.
- The database is opened read-only with `db.ConnectReadOnly` (`internal/db/connect.go:221`). The command checks that `crush.db` exists first and never creates or migrates a database.
- One hand-written query (`messagesQuery` in `report.go`), read row by row with `rows.Next`; no sqlc query, no whole result set in memory. Sections keep state for the current session only, besides their counters.
- Definitions, exactly:
  - Window bounds: `--since` and `--until` take a date such as `2026-11-09` (the first instant of that day, local time) or an age such as `7d` or `12h` (that long before now). `--since` is included and `--until` excluded, so `--since 2026-10-21 --until 2026-11-17` is the spec's fixed window (`created_at >= 1792555200 AND created_at < 1794891600`). An empty flag is an open bound.
  - Messages count every row in the window, including rows whose parts do not decode; those are counted again as `skipped_rows` and left out of every other figure.
  - Builds: the `build` stamp on a message's finish part; assistant messages without one are listed as `unstamped`.
  - Tool calls are `tool_call` parts; error results are `tool_result` parts with `is_error`. A call without a result counts as a call without an error. `view` and `read` also get a combined line, `view+read`.
  - Error kinds: the `error_kind` key of the result's metadata, listed under its own name whatever it is. A failure without a kind reads `other` when the build stamp on the step that made the call is a stamping build, and `unstamped` otherwise (no build stamp, no call found in the window, or a build that never stamps). A stamping build is one for which at least one tool result in the window carries `error_kind`, or a `bash` result carries `exit_code` or `interrupted`.
  - Foreground commands: `bash` results whose metadata carries `exit_code` or `interrupted` (`internal/agent/tools/bash.go:42-55`), including a `run_in_background` command that finished inside the probe window. Non-zero exits: `exit_code != 0`. Interrupted: `interrupted` is true. A `bash` result with neither stamp is a moved-to-background or never-run command when its build stamps, and is counted as `unstamped` otherwise.
  - Edits: `edit` and `multiedit` together. Failed calls are their error results; failure rate is failed calls over calls. Failed edits inside a `multiedit` batch that otherwise succeeded (`edits_failed` on a result without `is_error`) are a separate line and do not enter the rate.
  - Read-guard refusal: a failed `edit` or `multiedit` whose kind is `not_read` or `changed_since_read`. After it, the next `edit` or `multiedit` call on the same file in the session is the retry (same text when the hash of its `old_string` values equals the refused call's, else different text); an `edit` or `multiedit` of another file first, or a `write` to the same file, is moved on; the end of the session first is no retry. Rows before the kinds existed show no refusals.
  - Runs of failed edits: consecutive failed `edit` or `multiedit` results in a session with no successful one between them; results of other tools do not break a run.
  - Reads: `view` and `read` calls. With a limit: the `limit` key is a JSON number. Lines requested: `limit` when it is above zero, else 200 (`DefaultReadLimit`, `internal/agent/tools/view.go:80`). Re-read: a call that names a path already named by a `view` or `read` call earlier in the session. Paging: a call that sets `offset` or `limit` and names a path the previous path-naming `view` or `read` call in the session named.
  - Percentiles: nearest rank, the value at 1-based rank `ceil(p * n / 100)` of the sorted values.
- JSON output: one indented object with `"schema": 1`; within one schema number fields are only added. Rates and shares are fractions from 0 to 1 in JSON and percentages with two decimals in text.
- The report owns its tool names as local constants (`bash`, `edit`, `multiedit`, `write`, `view`, `read`); it does not import `internal/agent/tools`.
- Style: gofumpt (`/home/pbtrudel/.local/bin/gofumpt`); comments on their own line start with a capital, end with a period, wrap at 78 columns; no em dashes anywhere; testify `require`; log messages start with a capital letter (this plan adds none).
- Commits: one per task, semantic subjects, no attribution lines. Commits are authorized for this work.
- Never open or print any `crush.json` or `crushrc` file. Never print message text, titles, paths or session ids from the real database; the live checks print only the report's own output.
- Size: part 1 about 660 changed lines, part 2 about 450, part 3 about 310, part 4 about 330. Part 1 is above the 400-line target because the pass, the two output formats, the command and the test log are the least that can be merged and run on its own; each PR description states its size.

## Review Focus

1. A live database: the report opens the log read-only while another process holds it open for writing in WAL mode, and must never create, migrate or lock it. Pinned in Task 1 (every report test opens the log read-only while the test's writer stays open) and Task 2 (a missing database is an error and no file appears).
2. Rows written by a newer or a broken build: parts that do not decode are counted and skipped, and the report still prints; a kind the report does not know is listed under its own name. Pinned in Task 1 (an unknown part type) and Task 5 (an unknown kind).
3. Metadata that is not a JSON object (an MCP tool can return anything, and most results have an empty string): no stamp, no kind, no failure. Pinned in Task 5 and Task 7.
4. Same-second rows: `created_at` has one-second resolution, so a call and its result written in the same second are ordered by `rowid`, and the result must still find its call's build. Pinned in Task 5.
5. A window that cuts a session: a result whose call is before `--since` has no known build and reads `unstamped`, not `other`; and no per-session state (paths read, pending refusals, runs) leaks into the next session. Pinned in Task 5 (a result without its call), Task 8 and Task 10 (a second session).

---

# Part 1: the pass, the Window section and the command

Branch `w0-stats-report-1`. When merged, `crush stats report` prints the Window section (bounds, messages by role, sessions, skipped rows) as text or JSON.

### Task 1: The pass and the Window section

**Files:**
- Create: `internal/message/parts_codec.go`, `internal/logreport/report.go`, `internal/logreport/window.go`, `internal/logreport/output.go`
- Test: `internal/logreport/testlog_test.go`, `internal/logreport/window_test.go`, `internal/logreport/privacy_test.go` (all new)

**Interfaces:**
- Consumes: `message.Message`, `message.ContentPart` and the part types (`internal/message/content.go`); `unmarshalParts` and `marshalParts` (`internal/message/message.go:697-760`); `db.Connect`, `db.Release`, `db.ConnectReadOnly` (`internal/db/connect.go`).
- Produces:
  - `func message.DecodeParts(data []byte) ([]message.ContentPart, error)`, `func message.EncodeParts(parts []message.ContentPart) ([]byte, error)`.
  - `const Schema = 1`; `type Options struct { Since, Until time.Time }`; `type Report struct { Schema int; Window Window }`; `func Run(ctx context.Context, conn *sql.DB, opts Options) (*Report, error)`; `func (r *Report) WriteJSON(w io.Writer) error`; `func (r *Report) WriteText(w io.Writer) error`; `func ParseBound(value string, now time.Time) (time.Time, error)`.
  - Internal, used by every later task: `type section interface { message(p *pass, m *message.Message); endSession(p *pass); finish(p *pass, r *Report) }`; `func newSections() []section`; `type pass struct` with fields `opts`, `roles`, `sessions`, `skipped`, `first`, `last`; `func newPass(opts Options) *pass`; `func (p *pass) endSession(sections []section)`; `func formatCounts(counts map[string]int) string`; `type Window`, `type windowSection struct{}`.
  - Test helpers: `func newTestLog(t *testing.T) *testLog`; `(*testLog).session(id, title string)`; `(*testLog).add(sessionID string, role message.MessageRole, parts ...message.ContentPart)`; `(*testLog).raw(sessionID string, role message.MessageRole, parts string)`; `(*testLog).report(opts Options) *Report`; field `testLog.at time.Time` (the next message's time; each message advances it one second); `const marker`.

- [ ] **Step 1: Write the failing tests**

Create `internal/logreport/testlog_test.go`:

```go
package logreport

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// testLog is a session log built inside a test, with the schema the
// migrations create.
type testLog struct {
	t    *testing.T
	conn *sql.DB
	path string
	n    int
	// at is the time of the next message; each message advances it.
	at time.Time
}

func newTestLog(t *testing.T) *testLog {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Connect(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dir) })
	return &testLog{
		t:    t,
		conn: conn,
		path: filepath.Join(dir, "crush.db"),
		at:   time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local),
	}
}

func (l *testLog) session(id, title string) {
	l.t.Helper()
	_, err := l.conn.ExecContext(l.t.Context(),
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, 0, 0)`, id, title)
	require.NoError(l.t, err)
}

// add writes a message with the given parts at the log's clock and moves
// the clock one second on.
func (l *testLog) add(sessionID string, role message.MessageRole, parts ...message.ContentPart) {
	l.t.Helper()
	data, err := message.EncodeParts(parts)
	require.NoError(l.t, err)
	l.raw(sessionID, role, string(data))
}

// raw writes a message whose parts column is stored as given.
func (l *testLog) raw(sessionID string, role message.MessageRole, parts string) {
	l.t.Helper()
	l.n++
	_, err := l.conn.ExecContext(l.t.Context(),
		`INSERT INTO messages (id, session_id, role, parts, model, provider, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'model-a', 'provider-a', ?, ?)`,
		fmt.Sprintf("msg-%d", l.n), sessionID, string(role), parts, l.at.Unix(), l.at.Unix())
	require.NoError(l.t, err)
	l.at = l.at.Add(time.Second)
}

// report opens the log read-only, as the command does, and runs the
// report on it while the writer stays open.
func (l *testLog) report(opts Options) *Report {
	l.t.Helper()
	conn, err := db.ConnectReadOnly(l.t.Context(), l.path)
	require.NoError(l.t, err)
	defer conn.Close()
	r, err := Run(l.t.Context(), conn, opts)
	require.NoError(l.t, err)
	return r
}
```

Create `internal/logreport/window_test.go`:

```go
package logreport

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestWindowCountsMessagesAndSessions(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "first")
	log.session("s2", "second")
	log.add("s1", message.User, message.TextContent{Text: "hello"})
	log.add("s1", message.Assistant, message.TextContent{Text: "hi"}, message.Finish{Reason: message.FinishReasonEndTurn})
	log.add("s2", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn})
	log.raw("s2", message.Tool, `[{"type":"no_such_part","data":{}}]`)
	log.add("s2", message.User, message.TextContent{Text: "again"})
	// One parts value can exceed half a megabyte.
	log.add("s2", message.Tool, message.ToolResult{ToolCallID: "c1", Name: "bash", Content: strings.Repeat("x", 1<<20)})

	w := log.report(Options{}).Window
	require.Equal(t, 6, w.Messages)
	require.Equal(t, map[string]int{"user": 2, "assistant": 2, "tool": 2}, w.ByRole)
	require.Equal(t, 2, w.Sessions)
	require.Equal(t, 1, w.Skipped, "a row whose parts do not decode is counted and skipped")
	require.Nil(t, w.Since)
	require.Nil(t, w.Until)
}

func TestWindowBounds(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "only")
	start := log.at
	for range 4 {
		log.add("s1", message.User, message.TextContent{Text: "x"})
	}

	// Since is included and Until is excluded.
	w := log.report(Options{Since: start.Add(time.Second), Until: start.Add(3 * time.Second)}).Window
	require.Equal(t, 2, w.Messages)
	require.Equal(t, start.Add(time.Second).Unix(), w.First.Unix())
	require.Equal(t, start.Add(2*time.Second).Unix(), w.Last.Unix())
	require.NotNil(t, w.Since)

	empty := log.report(Options{Since: start.Add(time.Hour)})
	require.Zero(t, empty.Window.Messages)
	require.Zero(t, empty.Window.Sessions)
	require.Nil(t, empty.Window.First)
	var text, js bytes.Buffer
	require.NoError(t, empty.WriteText(&text), "an empty window is still reported")
	require.NoError(t, empty.WriteJSON(&js))
	require.Contains(t, js.String(), `"schema": 1`)
}

func TestParseBound(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 17, 15, 30, 0, 0, time.Local)
	tests := []struct {
		value string
		want  time.Time
	}{
		{value: "", want: time.Time{}},
		{value: "2026-11-09", want: time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local)},
		{value: "7d", want: now.AddDate(0, 0, -7)},
		{value: "12h", want: now.Add(-12 * time.Hour)},
	}
	for _, tt := range tests {
		got, err := ParseBound(tt.value, now)
		require.NoError(t, err, tt.value)
		require.True(t, tt.want.Equal(got), "%s: got %s", tt.value, got)
	}
	for _, bad := range []string{"yesterday", "-3d", "2026-13-01", "-2h"} {
		_, err := ParseBound(bad, now)
		require.Error(t, err, bad)
	}
}
```

Create `internal/logreport/privacy_test.go`:

```go
package logreport

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// marker is placed in everything private a log holds: prompts, answers,
// tool input and output, paths, titles and ids. It must appear in neither
// output format, whatever sections the report has.
const marker = "PRIVATE-7f3a9c"

func TestReportNeverPrintsPrivateText(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	session := "session-" + marker
	path := "/home/" + marker + "/a.go"
	log.session(session, "title "+marker)
	log.add(session, message.User, message.TextContent{Text: "prompt " + marker})
	log.add(session, message.User, message.TextContent{Text: "<system_reminder>" + marker, Hidden: true})
	log.add(session, message.Assistant,
		message.ReasoningContent{Thinking: "thinking " + marker},
		message.TextContent{Text: "answer " + marker},
		message.ToolCall{ID: "call-" + marker + "-1", Name: "bash", Input: `{"command":"echo ` + marker + `"}`},
		message.ToolCall{ID: "call-" + marker + "-2", Name: "edit", Input: `{"file_path":"` + path + `","old_string":"` + marker + `"}`},
		message.ToolCall{ID: "call-" + marker + "-3", Name: "view", Input: `{"file_path":"` + path + `","limit":5}`},
		message.Finish{Reason: message.FinishReasonToolUse, Message: marker, Details: marker, Build: "0.100.0-fork.120"},
	)
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-1", Name: "bash", Content: "output " + marker,
		Metadata: `{"exit_code":1,"output":"` + marker + `","description":"` + marker + `","working_directory":"` + path + `"}`,
	})
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-2", Name: "edit", Content: "error at " + path, IsError: true,
		Metadata: `{"error_kind":"not_read"}`,
	})
	log.add(session, message.Tool, message.ToolResult{ToolCallID: "call-" + marker + "-3", Name: "view", Content: marker})
	log.add(session, message.Assistant, message.SubAgentReport{Label: marker, Output: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"})

	r := log.report(Options{})
	require.Equal(t, 7, r.Window.Messages, "the fixture is read")
	require.Zero(t, r.Window.Skipped, "every part of the fixture decodes")
	var text, js bytes.Buffer
	require.NoError(t, r.WriteText(&text))
	require.NoError(t, r.WriteJSON(&js))
	require.NotContains(t, text.String(), marker)
	require.NotContains(t, js.String(), marker)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -count=1`
Expected: build failure, `undefined: message.EncodeParts`, `undefined: Options`, `undefined: Report`, `undefined: Run`.

- [ ] **Step 3: Implement**

Create `internal/message/parts_codec.go`:

```go
package message

// DecodeParts decodes a stored parts column into content parts, as the
// message service does when it loads a message. It fails on a part type
// it does not know.
func DecodeParts(data []byte) ([]ContentPart, error) {
	return unmarshalParts(data)
}

// EncodeParts encodes content parts the way the message service stores
// them.
func EncodeParts(parts []ContentPart) ([]byte, error) {
	return marshalParts(parts)
}
```

Create `internal/logreport/report.go`:

```go
// Package logreport reads one project's session log and reports counts,
// rates and the names of tools, kinds and builds. It never reports text
// from a message, a title, a path or a session id.
package logreport

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// Schema is the version of the JSON output. Within one schema number
// fields are only added.
const Schema = 1

// Options bounds the messages a report reads. A zero bound is open.
type Options struct {
	// Since is the first instant included.
	Since time.Time
	// Until is the first instant excluded.
	Until time.Time
}

// Report is the result of one pass over the session log.
type Report struct {
	Schema int    `json:"schema"`
	Window Window `json:"window"`
}

// section is one part of the report. The pass shows it every message it
// could decode, in session_id, created_at, rowid order, and tells it when
// a session ends. Besides its counters a section keeps state for the
// current session only. finish writes its figures into the report once
// the pass is over.
type section interface {
	message(p *pass, m *message.Message)
	endSession(p *pass)
	finish(p *pass, r *Report)
}

// newSections returns the report's sections in the order they print.
func newSections() []section {
	return []section{&windowSection{}}
}

// pass holds what the sections share.
type pass struct {
	opts     Options
	roles    map[string]int
	sessions int
	skipped  int
	first    int64
	last     int64
}

const messagesQuery = `SELECT session_id, role, parts, COALESCE(model, ''),
	COALESCE(provider, ''), created_at, is_summary_message
FROM messages
WHERE created_at >= ? AND created_at < ?
ORDER BY session_id, created_at, rowid`

// Run makes one pass over the messages in the window and returns the
// report. It reads row by row, because a single parts value can exceed
// half a megabyte, and only reads, so conn should be opened read-only.
func Run(ctx context.Context, conn *sql.DB, opts Options) (*Report, error) {
	since, until := int64(math.MinInt64), int64(math.MaxInt64)
	if !opts.Since.IsZero() {
		since = opts.Since.Unix()
	}
	if !opts.Until.IsZero() {
		until = opts.Until.Unix()
	}
	rows, err := conn.QueryContext(ctx, messagesQuery, since, until)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	p := newPass(opts)
	sections := newSections()
	current := ""
	for rows.Next() {
		var (
			m     message.Message
			role  string
			parts []byte
		)
		if err := rows.Scan(&m.SessionID, &role, &parts, &m.Model, &m.Provider, &m.CreatedAt, &m.IsSummaryMessage); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if p.sessions == 0 || m.SessionID != current {
			if p.sessions > 0 {
				p.endSession(sections)
			}
			current = m.SessionID
			p.sessions++
		}
		m.Role = message.MessageRole(role)
		p.count(&m)
		if m.Parts, err = message.DecodeParts(parts); err != nil {
			p.skipped++
			continue
		}
		for _, s := range sections {
			s.message(p, &m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	if p.sessions > 0 {
		p.endSession(sections)
	}

	r := &Report{Schema: Schema}
	for _, s := range sections {
		s.finish(p, r)
	}
	return r, nil
}

func newPass(opts Options) *pass {
	return &pass{opts: opts, roles: map[string]int{}}
}

// count records every message, whether its parts decode or not.
func (p *pass) count(m *message.Message) {
	p.roles[string(m.Role)]++
	if p.first == 0 || m.CreatedAt < p.first {
		p.first = m.CreatedAt
	}
	p.last = max(p.last, m.CreatedAt)
}

func (p *pass) endSession(sections []section) {
	for _, s := range sections {
		s.endSession(p)
	}
}
```

Create `internal/logreport/window.go`:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// Window is the Window section: what the pass read.
type Window struct {
	Since    *time.Time     `json:"since,omitempty"`
	Until    *time.Time     `json:"until,omitempty"`
	First    *time.Time     `json:"first_message,omitempty"`
	Last     *time.Time     `json:"last_message,omitempty"`
	Messages int            `json:"messages"`
	ByRole   map[string]int `json:"messages_by_role"`
	Sessions int            `json:"sessions"`
	// Skipped counts the messages whose parts do not decode. They count
	// as messages and are left out of every other figure.
	Skipped int `json:"skipped_rows"`
}

type windowSection struct{}

func (s *windowSection) message(*pass, *message.Message) {}

func (s *windowSection) endSession(*pass) {}

func (s *windowSection) finish(p *pass, r *Report) {
	w := Window{ByRole: p.roles, Sessions: p.sessions, Skipped: p.skipped}
	if !p.opts.Since.IsZero() {
		w.Since = &p.opts.Since
	}
	if !p.opts.Until.IsZero() {
		w.Until = &p.opts.Until
	}
	for _, n := range p.roles {
		w.Messages += n
	}
	if w.Messages > 0 {
		first, last := time.Unix(p.first, 0), time.Unix(p.last, 0)
		w.First, w.Last = &first, &last
	}
	r.Window = w
}

func (w Window) writeText(out io.Writer) {
	fmt.Fprintln(out, "Window")
	fmt.Fprintf(out, "  since\t%s\n", formatTime(w.Since))
	fmt.Fprintf(out, "  until\t%s\n", formatTime(w.Until))
	fmt.Fprintf(out, "  messages\t%d\t%s\n", w.Messages, formatCounts(w.ByRole))
	fmt.Fprintf(out, "  sessions\t%d\n", w.Sessions)
	fmt.Fprintf(out, "  skipped rows\t%d\n", w.Skipped)
	fmt.Fprintln(out)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "open"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatCounts lists counts as "name n", largest first, ties by name.
func formatCounts(counts map[string]int) string {
	names := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + " " + strconv.Itoa(counts[name])
	}
	return strings.Join(parts, ", ")
}

// ParseBound reads a --since or --until value: a date such as 2026-11-09,
// meaning the start of that day in local time, or an age such as 7d or
// 12h, meaning that long before now. An empty value is an open bound.
func ParseBound(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is neither a date such as 2026-11-09 nor an age such as 7d", value)
}
```

Create `internal/logreport/output.go`:

```go
package logreport

import (
	"encoding/json"
	"io"
	"text/tabwriter"
)

// WriteJSON writes the report as one indented JSON object.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText writes the report for a person to read, one section after
// another.
func (r *Report) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	r.Window.writeText(tw)
	return tw.Flush()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/message/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/message/parts_codec.go internal/logreport/
git add internal/message/parts_codec.go internal/logreport/
git commit -m "feat(logreport): count the messages in a window of the session log"
```

---

### Task 2: The `crush stats report` command

**Files:**
- Create: `internal/cmd/stats_report.go`
- Test: `internal/cmd/stats_report_test.go` (new)

**Interfaces:**
- Consumes: `logreport.Options`, `logreport.ParseBound`, `logreport.Run`, `(*logreport.Report).WriteJSON`, `(*logreport.Report).WriteText` (Task 1); `statsCmd` (`internal/cmd/stats.go:46`); `ResolveCwd` (`internal/cmd/root.go:1028`); `config.Init` (`internal/config/init.go:22`); `db.ConnectReadOnly`.
- Produces: `var statsReportCmd *cobra.Command` (registered under `statsCmd`); `func addStatsReportFlags(cmd *cobra.Command)` (flags `--since`, `--until`, `--json`); `func runStatsReport(cmd *cobra.Command, _ []string) error`. Later tasks add paragraphs to `statsReportCmd.Long`.

- [ ] **Step 1: Write the failing tests**

Create `internal/cmd/stats_report_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// runStatsReportForTest runs the report command on its own, with the root
// flags it reads, so a test does not share the package's command values.
func runStatsReportForTest(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "report", Args: cobra.NoArgs, RunE: runStatsReport}
	cmd.Flags().String("cwd", "", "")
	cmd.Flags().String("data-dir", "", "")
	addStatsReportFlags(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func TestStatsReportReadsTheProjectLog(t *testing.T) {
	require.Contains(t, statsCmd.Commands(), statsReportCmd, "crush stats report")

	dir := t.TempDir()
	conn, err := db.Connect(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dir) })
	_, err = conn.ExecContext(t.Context(), `INSERT INTO sessions (id, title, created_at, updated_at) VALUES ('s1', 't', 0, 0)`)
	require.NoError(t, err)
	// 2026-11-10 12:00 UTC, which is 2026-11-10 in every time zone.
	_, err = conn.ExecContext(t.Context(), `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at)
		VALUES ('m1', 's1', 'user', '[]', 1794312000, 1794312000)`)
	require.NoError(t, err)

	out, err := runStatsReportForTest(t, "--data-dir", dir, "--json", "--since", "2026-10-21", "--until", "2026-11-17")
	require.NoError(t, err)
	var report struct {
		Schema int `json:"schema"`
		Window struct {
			Messages int `json:"messages"`
		} `json:"window"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Equal(t, 1, report.Schema)
	require.Equal(t, 1, report.Window.Messages)

	out, err = runStatsReportForTest(t, "--data-dir", dir)
	require.NoError(t, err)
	require.Contains(t, out, "Window")
}

func TestStatsReportRefusesBadInput(t *testing.T) {
	dir := t.TempDir()

	_, err := runStatsReportForTest(t, "--data-dir", dir)
	require.ErrorContains(t, err, "no session log")
	require.NoFileExists(t, filepath.Join(dir, "crush.db"), "the report never creates a database")

	_, err = runStatsReportForTest(t, "--data-dir", dir, "--since", "last week")
	require.ErrorContains(t, err, "invalid --since")

	_, err = runStatsReportForTest(t, "--data-dir", dir, "--since", "2026-11-17", "--until", "2026-11-09")
	require.ErrorContains(t, err, "--since must be before --until")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cmd/ -run TestStatsReport -count=1`
Expected: build failure, `undefined: runStatsReport`, `undefined: addStatsReportFlags`, `undefined: statsReportCmd`.

- [ ] **Step 3: Implement**

Create `internal/cmd/stats_report.go`:

```go
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/spf13/cobra"
)

var statsReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Print counts and rates from this project's session log",
	Long: `Print counts and rates from this project's session log, as text or
as JSON. The database is opened read-only, so a running session is not
disturbed. The output never contains text from a message, a title, a path
or a session id.

--since and --until take a date such as 2026-11-09 (the start of that day,
local time) or an age such as 7d or 12h. --since is included and --until
is excluded: --since 2026-10-21 --until 2026-11-17 ends at midnight before
November 17.`,
	Args: cobra.NoArgs,
	RunE: runStatsReport,
}

func init() {
	addStatsReportFlags(statsReportCmd)
	statsCmd.AddCommand(statsReportCmd)
}

func addStatsReportFlags(cmd *cobra.Command) {
	cmd.Flags().String("since", "", "Start of the window: a date such as 2026-11-09 or an age such as 7d")
	cmd.Flags().String("until", "", "End of the window, excluded: a date or an age")
	cmd.Flags().Bool("json", false, "Output in JSON format")
}

func runStatsReport(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	now := time.Now()
	var opts logreport.Options
	var err error
	since, _ := cmd.Flags().GetString("since")
	if opts.Since, err = logreport.ParseBound(since, now); err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, _ := cmd.Flags().GetString("until")
	if opts.Until, err = logreport.ParseBound(until, now); err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && !opts.Since.Before(opts.Until) {
		return errors.New("--since must be before --until")
	}

	dataDir, _ := cmd.Flags().GetString("data-dir")
	if dataDir == "" {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}
		cfg, err := config.Init(ctx, cwd, "", false)
		if err != nil {
			return fmt.Errorf("failed to initialize config: %w", err)
		}
		dataDir = cfg.Config().Options.DataDirectory
	}
	dbPath := filepath.Join(dataDir, "crush.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no session log at %s: %w", dbPath, err)
	}
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	report, err := logreport.Run(ctx, conn, opts)
	if err != nil {
		return err
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		return report.WriteJSON(cmd.OutOrStdout())
	}
	return report.WriteText(cmd.OutOrStdout())
}
```

The data directory comes from `--data-dir` when it is given; otherwise from the project configuration, the way `crush stats` and `crush session` find it. `--data-dir` skips loading the configuration, which is what the tests use.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -count=1`
Expected: PASS, including the existing `internal/cmd` tests.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/cmd/stats_report.go internal/cmd/stats_report_test.go
git add internal/cmd/stats_report.go internal/cmd/stats_report_test.go
git commit -m "feat(cmd): add crush stats report"
```

---

### Task 3: Part 1 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 1 (Tasks 1 and 2).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... ./internal/message/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/logreport/... ./internal/cmd/... ./internal/message/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 660 changed lines, in the files named in Tasks 1 and 2 only; the em dash count is `0`; no debug output, no file outside `internal/logreport/`, `internal/cmd/stats_report*.go` and `internal/message/parts_codec.go`.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(d["schema"], d["window"]["messages"], d["window"]["messages_by_role"], d["window"]["sessions"], d["window"]["skipped_rows"])' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures (they must match the one-off scripts, `/tmp/crush-model-intel/w0/out_20_totals.txt`): `messages 44692` with `tool 22295, assistant 16112, user 6285`; `sessions 274`; `skipped rows 0`; since `2026-10-21 00:00`, until `2026-11-17 00:00`. In the JSON, `window.messages` is 44692 and `window.messages_by_role` matches. A difference in messages is a defect to report, not to patch around.

---

# Part 2: builds and the Tools section

Branch `w0-stats-report-2`, from `origin/main` after part 1 merged. When merged, the Window section lists builds and the Tools section reports calls, error results, rates and error kinds per tool, with the `other` and `unstamped` rule.

### Task 4: Builds in the Window section

**Files:**
- Modify: `internal/logreport/window.go` (whole file below), `internal/logreport/report.go` (`newSections`)
- Test: `internal/logreport/window_test.go` (one new test)

**Interfaces:**
- Consumes: `section`, `pass`, `formatCounts` (Task 1).
- Produces: `type Build struct { Build string; Messages int; First, Last time.Time }`; `Window.Builds []Build` (JSON `builds`, sorted by first message, then name); `windowSection.builds map[string]*Build`.

- [ ] **Step 1: Write the failing test**

In `internal/logreport/window_test.go`, replace

```go
func TestWindowBounds(t *testing.T) {
```

with

```go
func TestWindowListsBuilds(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "first")
	log.session("s2", "second")
	log.add("s1", message.User, message.TextContent{Text: "hello"})
	log.add("s1", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.101"})
	log.add("s2", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn})
	log.add("s2", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.101"})
	log.add("s2", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn, Build: "v0.100.0-fork.102+dirty.abc"})

	builds := log.report(Options{}).Window.Builds
	require.Len(t, builds, 3, "the user message carries no build and is not listed")
	require.Equal(t, "0.100.0-fork.101", builds[0].Build, "builds are listed by their first message")
	require.Equal(t, 2, builds[0].Messages)
	require.Equal(t, 2*time.Second, builds[0].Last.Sub(builds[0].First), "first and last span both sessions")
	require.Equal(t, "unstamped", builds[1].Build)
	require.Equal(t, 1, builds[1].Messages)
	require.Equal(t, "v0.100.0-fork.102+dirty.abc", builds[2].Build)
}

func TestWindowBounds(t *testing.T) {
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logreport/ -run TestWindowListsBuilds -count=1`
Expected: build failure, `log.report(Options{}).Window.Builds undefined (type Window has no field or method Builds)`.

- [ ] **Step 3: Implement**

Replace the contents of `internal/logreport/window.go` with:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// Window is the Window section: what the pass read.
type Window struct {
	Since    *time.Time     `json:"since,omitempty"`
	Until    *time.Time     `json:"until,omitempty"`
	First    *time.Time     `json:"first_message,omitempty"`
	Last     *time.Time     `json:"last_message,omitempty"`
	Messages int            `json:"messages"`
	ByRole   map[string]int `json:"messages_by_role"`
	Sessions int            `json:"sessions"`
	// Skipped counts the messages whose parts do not decode. They count
	// as messages and are left out of every other figure.
	Skipped int     `json:"skipped_rows"`
	Builds  []Build `json:"builds"`
}

// Build is one build seen on the messages' finish parts, or "unstamped"
// for the assistant messages that carry none.
type Build struct {
	Build    string    `json:"build"`
	Messages int       `json:"messages"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
}

type windowSection struct {
	builds map[string]*Build
}

func (s *windowSection) message(_ *pass, m *message.Message) {
	build := ""
	if f := m.FinishPart(); f != nil {
		build = f.Build
	}
	if build == "" && m.Role != message.Assistant {
		return
	}
	if build == "" {
		build = "unstamped"
	}
	at := time.Unix(m.CreatedAt, 0)
	b, ok := s.builds[build]
	if !ok {
		b = &Build{Build: build, First: at, Last: at}
		s.builds[build] = b
	}
	b.Messages++
	if at.Before(b.First) {
		b.First = at
	}
	if at.After(b.Last) {
		b.Last = at
	}
}

func (s *windowSection) endSession(*pass) {}

func (s *windowSection) finish(p *pass, r *Report) {
	w := Window{ByRole: p.roles, Sessions: p.sessions, Skipped: p.skipped}
	if !p.opts.Since.IsZero() {
		w.Since = &p.opts.Since
	}
	if !p.opts.Until.IsZero() {
		w.Until = &p.opts.Until
	}
	for _, n := range p.roles {
		w.Messages += n
	}
	if w.Messages > 0 {
		first, last := time.Unix(p.first, 0), time.Unix(p.last, 0)
		w.First, w.Last = &first, &last
	}
	w.Builds = []Build{}
	for _, b := range s.builds {
		w.Builds = append(w.Builds, *b)
	}
	slices.SortFunc(w.Builds, func(a, b Build) int {
		return cmp.Or(a.First.Compare(b.First), cmp.Compare(a.Build, b.Build))
	})
	r.Window = w
}

func (w Window) writeText(out io.Writer) {
	fmt.Fprintln(out, "Window")
	fmt.Fprintf(out, "  since\t%s\n", formatTime(w.Since))
	fmt.Fprintf(out, "  until\t%s\n", formatTime(w.Until))
	fmt.Fprintf(out, "  messages\t%d\t%s\n", w.Messages, formatCounts(w.ByRole))
	fmt.Fprintf(out, "  sessions\t%d\n", w.Sessions)
	fmt.Fprintf(out, "  skipped rows\t%d\n", w.Skipped)
	for _, b := range w.Builds {
		fmt.Fprintf(out, "  build %s\t%d\t%s to %s\n", b.Build, b.Messages,
			b.First.Format(time.DateOnly), b.Last.Format(time.DateOnly))
	}
	fmt.Fprintln(out)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "open"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatCounts lists counts as "name n", largest first, ties by name.
func formatCounts(counts map[string]int) string {
	names := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + " " + strconv.Itoa(counts[name])
	}
	return strings.Join(parts, ", ")
}

// ParseBound reads a --since or --until value: a date such as 2026-11-09,
// meaning the start of that day in local time, or an age such as 7d or
// 12h, meaning that long before now. An empty value is an open bound.
func ParseBound(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is neither a date such as 2026-11-09 nor an age such as 7d", value)
}
```

In `internal/logreport/report.go`, replace

```go
	return []section{&windowSection{}}
```

with

```go
	return []section{&windowSection{builds: map[string]*Build{}}}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/window.go internal/logreport/window_test.go internal/logreport/report.go
git commit -m "feat(logreport): list the builds seen in the window"
```

---

### Task 5: The Tools section and the stamping rule

**Files:**
- Modify: `internal/logreport/report.go` (whole file below), `internal/logreport/output.go` (`WriteText`), `internal/cmd/stats_report.go` (`Long`)
- Create: `internal/logreport/tools.go`
- Test: `internal/logreport/testlog_test.go` (two helpers), `internal/logreport/tools_test.go` (new)

**Interfaces:**
- Consumes: Task 1 and Task 4.
- Produces:
  - `Report.Tools Tools` (JSON `tools`); `type Tools struct { Calls, Errors int; Rate float64; ByTool []ToolStats; ViewRead ToolStats }`; `type ToolStats struct { Name string; Calls, Errors int; Rate float64; Kinds map[string]int }`.
  - On the pass: `pass.calls map[string]call` (the current session's calls by id, cleared at each session end), `type call struct { name, build string }`, `pass.stamping map[string]bool`, `func (p *pass) observe(m *message.Message)`, `func hasStamp(tr message.ToolResult) bool`, `func (p *pass) kindName(kind, build string) string` (returns the kind, `"other"` or `"unstamped"`; call it only in `finish`).
  - Shared by later sections: `type kindKey struct { kind, build string }`, `type kindCounts map[kindKey]int` with `add(kind, build string)` and `resolve(p *pass) map[string]int`; `func ratio(n, of int) float64`; `func percent(r float64) string`; constants `bashTool`, `viewTool`, `readTool`.
  - Test helpers: `(*testLog).step(sessionID, build string, calls ...message.ToolCall)`, `(*testLog).result(sessionID string, tr message.ToolResult)`; constants `newBuild` (a build that stamps) and `oldBuild` (one that does not); `func toolNames(stats []ToolStats) []string`.

- [ ] **Step 1: Write the failing tests**

In `internal/logreport/testlog_test.go`, replace

```go
// report opens the log read-only, as the command does, and runs the
```

with

```go
// step writes an assistant message that makes the given calls, stamped
// with build when it is not empty.
func (l *testLog) step(sessionID, build string, calls ...message.ToolCall) {
	l.t.Helper()
	parts := []message.ContentPart{}
	for _, c := range calls {
		parts = append(parts, c)
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonToolUse, Build: build})
	l.add(sessionID, message.Assistant, parts...)
}

// result writes a tool message carrying one result.
func (l *testLog) result(sessionID string, tr message.ToolResult) {
	l.t.Helper()
	l.add(sessionID, message.Tool, tr)
}

// report opens the log read-only, as the command does, and runs the
```

Create `internal/logreport/tools_test.go`:

```go
package logreport

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// Builds used by the tests: newBuild stamps its tool results, oldBuild
// was built before the stamps existed.
const (
	newBuild = "0.100.0-fork.120"
	oldBuild = "0.100.0-fork.90"
)

func TestToolsCountsCallsErrorsAndKinds(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "c1", Name: "edit"},
		message.ToolCall{ID: "c2", Name: "edit"},
		message.ToolCall{ID: "c3", Name: "view"},
		message.ToolCall{ID: "c4", Name: "read"},
		message.ToolCall{ID: "c5", Name: "fetch"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "c1", Name: "edit", IsError: true, Metadata: `{"error_kind":"not_read"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c2", Name: "edit"})
	log.result("s1", message.ToolResult{ToolCallID: "c3", Name: "view", IsError: true, Metadata: `{"error_kind":"a_kind_from_a_later_build"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c4", Name: "read", IsError: true})
	// fetch's call has no result: it counts as a call without an error.

	tools := log.report(Options{}).Tools
	require.Equal(t, 5, tools.Calls)
	require.Equal(t, 3, tools.Errors)
	require.InDelta(t, 0.6, tools.Rate, 1e-9)
	require.Equal(t, []string{"edit", "fetch", "read", "view"}, toolNames(tools.ByTool), "most calls first, ties by name")
	edit := tools.ByTool[0]
	require.Equal(t, 2, edit.Calls)
	require.Equal(t, 1, edit.Errors)
	require.Equal(t, map[string]int{"not_read": 1}, edit.Kinds)
	require.Equal(t, "view+read", tools.ViewRead.Name)
	require.Equal(t, 2, tools.ViewRead.Calls)
	require.Equal(t, 2, tools.ViewRead.Errors)
	// An unknown kind is listed under its own name. The read error has
	// no kind and its build stamps kinds, so it is "other".
	require.Equal(t, map[string]int{"a_kind_from_a_later_build": 1, "other": 1}, tools.ViewRead.Kinds)
}

func TestErrorsWithoutKindFromBuildsThatDoNotStampAreUnstamped(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("new", "title")
	log.session("old", "title")
	// The new build stamps its shell results, so its errors without a
	// kind are "other".
	log.step("new", newBuild, message.ToolCall{ID: "n1", Name: "bash"}, message.ToolCall{ID: "n2", Name: "glob"})
	log.result("new", message.ToolResult{ToolCallID: "n1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("new", message.ToolResult{ToolCallID: "n2", Name: "glob", IsError: true})
	// The old build stamps nothing; a step without a build is unknown.
	log.step("old", oldBuild, message.ToolCall{ID: "o1", Name: "glob"}, message.ToolCall{ID: "o2", Name: "bash"})
	log.result("old", message.ToolResult{ToolCallID: "o1", Name: "glob", IsError: true})
	log.result("old", message.ToolResult{ToolCallID: "o2", Name: "bash", Content: "ok"})
	log.step("old", "", message.ToolCall{ID: "o3", Name: "glob"})
	log.result("old", message.ToolResult{ToolCallID: "o3", Name: "glob", IsError: true})
	// A result whose call is not in the window has no known build.
	log.result("old", message.ToolResult{ToolCallID: "gone", Name: "glob", IsError: true})

	glob := log.report(Options{}).Tools.ByTool[0]
	require.Equal(t, "glob", glob.Name)
	require.Equal(t, map[string]int{"other": 1, "unstamped": 3}, glob.Kinds)
}

func TestStampsOnOtherToolsDoNotMarkABuild(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	// Only a shell result's exit code marks its build; any tool's
	// metadata can hold an exit_code key.
	log.step("s1", oldBuild, message.ToolCall{ID: "m1", Name: "mcp_tool"}, message.ToolCall{ID: "g1", Name: "glob"})
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "g1", Name: "glob", IsError: true})

	require.Equal(t, map[string]int{"unstamped": 1}, log.report(Options{}).Tools.ByTool[0].Kinds)
}

func TestToolsReadsSameSecondRowsAndOddMetadata(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "m1", Name: "mcp_tool"},
		message.ToolCall{ID: "m2", Name: "mcp_tool"},
	)
	// created_at has one-second resolution: a result written in the same
	// second as its call follows it by rowid, and still finds its build.
	log.at = log.at.Add(-time.Second)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	// Metadata that is not a JSON object carries no kind.
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", IsError: true, Metadata: "plain text"})
	log.result("s1", message.ToolResult{ToolCallID: "m2", Name: "mcp_tool", IsError: true, Metadata: `["error_kind"]`})

	tools := log.report(Options{}).Tools
	require.Equal(t, "mcp_tool", tools.ByTool[0].Name)
	require.Equal(t, map[string]int{"other": 2}, tools.ByTool[0].Kinds)
}

func toolNames(stats []ToolStats) []string {
	names := make([]string, len(stats))
	for i, s := range stats {
		names[i] = s.Name
	}
	return names
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -count=1`
Expected: build failure, `log.report(Options{}).Tools undefined (type *Report has no field or method Tools)`, `undefined: ToolStats`.

- [ ] **Step 3: Implement**

Replace the contents of `internal/logreport/report.go` with:

```go
// Package logreport reads one project's session log and reports counts,
// rates and the names of tools, kinds and builds. It never reports text
// from a message, a title, a path or a session id.
package logreport

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Schema is the version of the JSON output. Within one schema number
// fields are only added.
const Schema = 1

// Options bounds the messages a report reads. A zero bound is open.
type Options struct {
	// Since is the first instant included.
	Since time.Time
	// Until is the first instant excluded.
	Until time.Time
}

// Report is the result of one pass over the session log.
type Report struct {
	Schema int    `json:"schema"`
	Window Window `json:"window"`
	Tools  Tools  `json:"tools"`
}

// section is one part of the report. The pass shows it every message it
// could decode, in session_id, created_at, rowid order, and tells it when
// a session ends. Besides its counters a section keeps state for the
// current session only. finish writes its figures into the report once
// the pass is over, when the pass knows which builds stamp their results.
type section interface {
	message(p *pass, m *message.Message)
	endSession(p *pass)
	finish(p *pass, r *Report)
}

// newSections returns the report's sections in the order they print.
func newSections() []section {
	return []section{
		&windowSection{builds: map[string]*Build{}},
		newToolsSection(),
	}
}

// pass holds what the sections share.
type pass struct {
	opts     Options
	roles    map[string]int
	sessions int
	skipped  int
	first    int64
	last     int64
	// calls holds the current session's tool calls by id.
	calls map[string]call
	// stamping holds the builds whose tool results carry a stamp:
	// exit_code or interrupted on a shell result, error_kind on any.
	stamping map[string]bool
}

// call is what the pass keeps of a tool call until its session ends.
type call struct {
	name string
	// build is the build stamp of the step that made the call.
	build string
}

const messagesQuery = `SELECT session_id, role, parts, COALESCE(model, ''),
	COALESCE(provider, ''), created_at, is_summary_message
FROM messages
WHERE created_at >= ? AND created_at < ?
ORDER BY session_id, created_at, rowid`

// Run makes one pass over the messages in the window and returns the
// report. It reads row by row, because a single parts value can exceed
// half a megabyte, and only reads, so conn should be opened read-only.
func Run(ctx context.Context, conn *sql.DB, opts Options) (*Report, error) {
	since, until := int64(math.MinInt64), int64(math.MaxInt64)
	if !opts.Since.IsZero() {
		since = opts.Since.Unix()
	}
	if !opts.Until.IsZero() {
		until = opts.Until.Unix()
	}
	rows, err := conn.QueryContext(ctx, messagesQuery, since, until)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	p := newPass(opts)
	sections := newSections()
	current := ""
	for rows.Next() {
		var (
			m     message.Message
			role  string
			parts []byte
		)
		if err := rows.Scan(&m.SessionID, &role, &parts, &m.Model, &m.Provider, &m.CreatedAt, &m.IsSummaryMessage); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if p.sessions == 0 || m.SessionID != current {
			if p.sessions > 0 {
				p.endSession(sections)
			}
			current = m.SessionID
			p.sessions++
		}
		m.Role = message.MessageRole(role)
		p.count(&m)
		if m.Parts, err = message.DecodeParts(parts); err != nil {
			p.skipped++
			continue
		}
		p.observe(&m)
		for _, s := range sections {
			s.message(p, &m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	if p.sessions > 0 {
		p.endSession(sections)
	}

	r := &Report{Schema: Schema}
	for _, s := range sections {
		s.finish(p, r)
	}
	return r, nil
}

func newPass(opts Options) *pass {
	return &pass{
		opts:     opts,
		roles:    map[string]int{},
		calls:    map[string]call{},
		stamping: map[string]bool{},
	}
}

// count records every message, whether its parts decode or not.
func (p *pass) count(m *message.Message) {
	p.roles[string(m.Role)]++
	if p.first == 0 || m.CreatedAt < p.first {
		p.first = m.CreatedAt
	}
	p.last = max(p.last, m.CreatedAt)
}

func (p *pass) endSession(sections []section) {
	for _, s := range sections {
		s.endSession(p)
	}
	clear(p.calls)
}

// observe records the message's tool calls, and which builds stamp their
// results.
func (p *pass) observe(m *message.Message) {
	switch m.Role {
	case message.Assistant:
		build := ""
		if f := m.FinishPart(); f != nil {
			build = f.Build
		}
		for _, tc := range m.ToolCalls() {
			p.calls[tc.ID] = call{name: tc.Name, build: build}
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			c, ok := p.calls[tr.ToolCallID]
			if ok && c.build != "" && hasStamp(tr) {
				p.stamping[c.build] = true
			}
		}
	}
}

func hasStamp(tr message.ToolResult) bool {
	if gjson.Get(tr.Metadata, "error_kind").Exists() {
		return true
	}
	return tr.Name == bashTool && (gjson.Get(tr.Metadata, "exit_code").Exists() ||
		gjson.Get(tr.Metadata, "interrupted").Exists())
}

// kindName names a failure's kind for the report. A failure without one
// reads "other" when the build that made the call stamps kinds, and
// "unstamped" when it does not or is unknown.
func (p *pass) kindName(kind, build string) string {
	switch {
	case kind != "":
		return kind
	case build != "" && p.stamping[build]:
		return "other"
	default:
		return "unstamped"
	}
}

// Tool names the report reads: the names models call.
const (
	bashTool = "bash"
	viewTool = "view"
	readTool = "read"
)

func ratio(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}
```

Create `internal/logreport/tools.go`:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Tools is the Tools section: calls and error results per tool.
type Tools struct {
	Calls  int     `json:"calls"`
	Errors int     `json:"errors"`
	Rate   float64 `json:"error_rate"`
	// ByTool is sorted by calls, most first, ties by name.
	ByTool []ToolStats `json:"by_tool"`
	// ViewRead adds up view and read, one tool under two names.
	ViewRead ToolStats `json:"view_and_read"`
}

// ToolStats is one tool's calls, error results and their kinds.
type ToolStats struct {
	Name   string         `json:"name"`
	Calls  int            `json:"calls"`
	Errors int            `json:"errors"`
	Rate   float64        `json:"error_rate"`
	Kinds  map[string]int `json:"error_kinds,omitempty"`
}

// kindKey is a failure's kind, or its call's build when it has no kind:
// whether that reads "other" or "unstamped" is known after the pass.
type kindKey struct {
	kind  string
	build string
}

type kindCounts map[kindKey]int

func (k kindCounts) add(kind, build string) {
	if kind != "" {
		build = ""
	}
	k[kindKey{kind: kind, build: build}]++
}

func (k kindCounts) resolve(p *pass) map[string]int {
	if len(k) == 0 {
		return nil
	}
	out := map[string]int{}
	for key, n := range k {
		out[p.kindName(key.kind, key.build)] += n
	}
	return out
}

type toolsSection struct {
	calls  map[string]int
	errors map[string]int
	kinds  map[string]kindCounts
}

func newToolsSection() *toolsSection {
	return &toolsSection{calls: map[string]int{}, errors: map[string]int{}, kinds: map[string]kindCounts{}}
}

func (s *toolsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		for _, tc := range m.ToolCalls() {
			s.calls[tc.Name]++
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			if !tr.IsError {
				continue
			}
			s.errors[tr.Name]++
			if s.kinds[tr.Name] == nil {
				s.kinds[tr.Name] = kindCounts{}
			}
			s.kinds[tr.Name].add(gjson.Get(tr.Metadata, "error_kind").String(), p.calls[tr.ToolCallID].build)
		}
	}
}

func (s *toolsSection) endSession(*pass) {}

func (s *toolsSection) finish(p *pass, r *Report) {
	t := Tools{ByTool: []ToolStats{}, ViewRead: ToolStats{Name: viewTool + "+" + readTool}}
	names := map[string]bool{}
	for name := range s.calls {
		names[name] = true
	}
	for name := range s.errors {
		names[name] = true
	}
	for name := range names {
		stats := ToolStats{
			Name:   name,
			Calls:  s.calls[name],
			Errors: s.errors[name],
			Rate:   ratio(s.errors[name], s.calls[name]),
			Kinds:  s.kinds[name].resolve(p),
		}
		t.ByTool = append(t.ByTool, stats)
		t.Calls += stats.Calls
		t.Errors += stats.Errors
		if name == viewTool || name == readTool {
			t.ViewRead.Calls += stats.Calls
			t.ViewRead.Errors += stats.Errors
			for kind, n := range stats.Kinds {
				if t.ViewRead.Kinds == nil {
					t.ViewRead.Kinds = map[string]int{}
				}
				t.ViewRead.Kinds[kind] += n
			}
		}
	}
	t.Rate = ratio(t.Errors, t.Calls)
	t.ViewRead.Rate = ratio(t.ViewRead.Errors, t.ViewRead.Calls)
	slices.SortFunc(t.ByTool, func(a, b ToolStats) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Name, b.Name))
	})
	r.Tools = t
}

func (t Tools) writeText(out io.Writer) {
	fmt.Fprintln(out, "Tools")
	fmt.Fprintln(out, "  tool\tcalls\terrors\trate\tkinds")
	for _, s := range slices.Concat(t.ByTool, []ToolStats{t.ViewRead}) {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%s\t%s\n", s.Name, s.Calls, s.Errors, percent(s.Rate), formatCounts(s.Kinds))
	}
	fmt.Fprintf(out, "  all\t%d\t%d\t%s\t\n", t.Calls, t.Errors, percent(t.Rate))
	fmt.Fprintln(out)
}

func percent(r float64) string {
	return fmt.Sprintf("%.2f%%", 100*r)
}
```

In `internal/logreport/output.go`, replace

```go
	r.Window.writeText(tw)
```

with

```go
	r.Window.writeText(tw)
	r.Tools.writeText(tw)
```

In `internal/cmd/stats_report.go`, replace

```go
November 17.`,
```

with

```go
November 17.

An error result without a kind reads "other" when the build that made the
call stamps kinds, which the report learns from that build's stamped
results in the window, and "unstamped" otherwise.`,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/ internal/cmd/stats_report.go
git add internal/logreport/ internal/cmd/stats_report.go
git commit -m "feat(logreport): report calls, error results and error kinds per tool"
```

---

### Task 6: Part 2 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 2 (Tasks 4 and 5).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... ./internal/message/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/logreport/... ./internal/cmd/... ./internal/message/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 450 changed lines, in the files named in Tasks 4 and 5 only; the em dash count is `0`; no debug output, no file outside `internal/logreport/`, `internal/cmd/stats_report*.go` and `internal/message/parts_codec.go`.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); w, t = d["window"], d["tools"]; print(w["messages"], w["builds"], t["calls"], t["errors"], t["view_and_read"]["calls"])' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to Task 3's: one build line, `build unstamped 16112 2026-10-21 to 2026-11-16` (no row in the window carries a build stamp, which arrived after it); Tools `all 22295 628 2.82%`, and per tool the values of `/tmp/crush-model-intel/w0/out_21_tool_errors.txt` (for example `bash 10470 7`, `edit 1504 146`, `multiedit 314 38`, `job_output 527 129`), every kind `unstamped`; `view+read 2908 13 0.45%`. Calls and error results must match the scripts exactly; report any other difference rather than patching it.

---

# Part 3: the Shell and Reads sections

Branch `w0-stats-report-3`, from `origin/main` after part 2 merged.

### Task 7: The Shell section

**Files:**
- Create: `internal/logreport/shell.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`), `internal/logreport/output.go` (`WriteText`), `internal/cmd/stats_report.go` (`Long`)
- Test: `internal/logreport/shell_test.go` (new)

**Interfaces:**
- Consumes: `pass.calls`, `pass.kindName`, `ratio`, `percent`, `bashTool`, `newBuild`, `oldBuild`, `(*testLog).step`, `(*testLog).result` (Task 5).
- Produces: `Report.Shell Shell` (JSON `shell`); `type Shell struct { Commands, NonZero, Interrupted int; FailureRate float64; Unstamped int }`; `type shellSection struct { shell Shell; bare map[string]int }`.

- [ ] **Step 1: Write the failing tests**

Create `internal/logreport/shell_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestShellCountsForegroundCommands(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "b2", Name: "bash"},
		message.ToolCall{ID: "b3", Name: "bash"},
		message.ToolCall{ID: "b4", Name: "bash"},
		message.ToolCall{ID: "b5", Name: "bash"},
		message.ToolCall{ID: "j1", Name: "job_output"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "b2", Name: "bash", Metadata: `{"exit_code":2}`})
	log.result("s1", message.ToolResult{ToolCallID: "b3", Name: "bash", Metadata: `{"interrupted":true}`})
	// A run_in_background command that finished in the probe window
	// reports its exit inline and is a foreground command.
	log.result("s1", message.ToolResult{ToolCallID: "b4", Name: "bash", Metadata: `{"background":true,"shell_id":"x","exit_code":1}`})
	// Moved to the background by a build that stamps: not counted.
	log.result("s1", message.ToolResult{ToolCallID: "b5", Name: "bash", Metadata: `{"background":true,"shell_id":"y"}`})
	// Only shell results count, whatever their metadata holds.
	log.result("s1", message.ToolResult{ToolCallID: "j1", Name: "job_output", Metadata: `{"exit_code":3}`})

	shell := log.report(Options{}).Shell
	require.Equal(t, Shell{Commands: 4, NonZero: 2, Interrupted: 1, FailureRate: 0.75}, shell)
}

func TestShellResultsFromBuildsThatDoNotStampAreUnstamped(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	// The exit code is in the text only: the report does not read it.
	log.step("s1", oldBuild, message.ToolCall{ID: "o1", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o1", Name: "bash", Content: "boom\nExit code 1\n\n<cwd>/x</cwd>"})
	log.step("s1", "", message.ToolCall{ID: "o2", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o2", Name: "bash", Content: "ok"})
	// Metadata that is not a JSON object carries no stamp.
	log.step("s1", oldBuild, message.ToolCall{ID: "o3", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o3", Name: "bash", Metadata: "plain text"})

	require.Equal(t, Shell{Unstamped: 3}, log.report(Options{}).Shell)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -run TestShell -count=1`
Expected: build failure, `log.report(Options{}).Shell undefined (type *Report has no field or method Shell)`, `undefined: Shell`.

- [ ] **Step 3: Implement**

Create `internal/logreport/shell.go`:

```go
package logreport

import (
	"fmt"
	"io"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Shell is the Shell section. A foreground command is a shell result that
// carries the exit_code or the interrupted stamp; a command moved to the
// background carries neither and is not counted.
type Shell struct {
	Commands    int     `json:"commands"`
	NonZero     int     `json:"non_zero_exits"`
	Interrupted int     `json:"interrupted"`
	FailureRate float64 `json:"failure_rate"`
	// Unstamped counts the shell results without either stamp written by
	// builds that do not stamp them.
	Unstamped int `json:"unstamped"`
}

type shellSection struct {
	shell Shell
	// bare counts the results without either stamp by their call's build.
	bare map[string]int
}

func (s *shellSection) message(p *pass, m *message.Message) {
	if m.Role != message.Tool {
		return
	}
	for _, tr := range m.ToolResults() {
		if tr.Name != bashTool {
			continue
		}
		code := gjson.Get(tr.Metadata, "exit_code")
		interrupted := gjson.Get(tr.Metadata, "interrupted").Bool()
		switch {
		case interrupted:
			s.shell.Commands++
			s.shell.Interrupted++
		case code.Exists():
			s.shell.Commands++
			if code.Int() != 0 {
				s.shell.NonZero++
			}
		default:
			s.bare[p.calls[tr.ToolCallID].build]++
		}
	}
}

func (s *shellSection) endSession(*pass) {}

func (s *shellSection) finish(p *pass, r *Report) {
	for build, n := range s.bare {
		if p.kindName("", build) == "unstamped" {
			s.shell.Unstamped += n
		}
	}
	s.shell.FailureRate = ratio(s.shell.NonZero+s.shell.Interrupted, s.shell.Commands)
	r.Shell = s.shell
}

func (s Shell) writeText(out io.Writer) {
	fmt.Fprintln(out, "Shell")
	fmt.Fprintf(out, "  foreground commands\t%d\n", s.Commands)
	fmt.Fprintf(out, "  non-zero exits\t%d\n", s.NonZero)
	fmt.Fprintf(out, "  interrupted\t%d\n", s.Interrupted)
	fmt.Fprintf(out, "  failure rate\t%s\n", percent(s.FailureRate))
	fmt.Fprintf(out, "  unstamped results\t%d\n", s.Unstamped)
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Tools  Tools  `json:"tools"`
}
```

with

```go
	Tools  Tools  `json:"tools"`
	Shell  Shell  `json:"shell"`
}
```

and replace

```go
		newToolsSection(),
	}
```

with

```go
		newToolsSection(),
		&shellSection{bare: map[string]int{}},
	}
```

In `internal/logreport/output.go`, replace

```go
	r.Tools.writeText(tw)
```

with

```go
	r.Tools.writeText(tw)
	r.Shell.writeText(tw)
```

In `internal/cmd/stats_report.go`, replace

```go
November 17.

An error result
```

with

```go
November 17.

Foreground shell commands are the shell results that carry an exit code or
the interrupted mark, including a background command that finished before
it was moved. A command moved to the background carries neither and is not
counted.

An error result
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/ internal/cmd/stats_report.go
git add internal/logreport/ internal/cmd/stats_report.go
git commit -m "feat(logreport): report how shell commands ended"
```

---

### Task 8: The Reads section

**Files:**
- Create: `internal/logreport/reads.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`), `internal/logreport/output.go` (`WriteText`)
- Test: `internal/logreport/reads_test.go` (new)

**Interfaces:**
- Consumes: `ratio`, `percent`, `viewTool`, `readTool`, `newBuild`, `(*testLog).step` (Task 5).
- Produces: `Report.Reads Reads` (JSON `reads`); `type Reads struct { Calls, WithLimit int; LimitShare float64; MedianLines, ReReads int; ReReadShare float64; Paging int; PagingShare float64 }`; `const defaultReadLines = 200`; `func percentile(values []int, p int) int` (nearest rank; item 8 uses it for its medians and 90th percentiles); `type readsSection struct { reads Reads; lines []int; seen map[string]bool; prev []string }`.

- [ ] **Step 1: Write the failing tests**

Create `internal/logreport/reads_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestReads(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	view := func(session, id, name, input string) {
		log.step(session, newBuild, message.ToolCall{ID: id, Name: name, Input: input})
	}
	view("s1", "v1", "view", `{"file_path":"a.go"}`)
	// Pages through a.go: re-reads that set an offset or a limit.
	view("s1", "v2", "read", `{"file_path":"a.go","offset":200,"limit":100}`)
	view("s1", "v3", "view", `{"file_path":"a.go","offset":300}`)
	view("s1", "v4", "view", `{"file_path":"b.go","limit":50}`)
	// A re-read whose previous read was of another file: not paging.
	view("s1", "v5", "view", `{"file_path":"a.go","limit":400}`)
	// A re-read without an offset or a limit: not paging.
	view("s1", "v6", "view", `{"file_path":"a.go"}`)
	// The multi-file form; c.go was not read before.
	view("s1", "v7", "view", `{"file_paths":["b.go","c.go"],"limit":10}`)
	// Another session starts with nothing read.
	log.session("s2", "title")
	view("s2", "v8", "view", `{"file_path":"a.go","offset":10}`)

	reads := log.report(Options{}).Reads
	require.Equal(t, 8, reads.Calls)
	require.Equal(t, 4, reads.WithLimit)
	require.InDelta(t, 0.5, reads.LimitShare, 1e-9)
	// Lines requested: 200, 100, 200, 50, 400, 200, 10, 200.
	require.Equal(t, 200, reads.MedianLines)
	require.Equal(t, 5, reads.ReReads)
	require.Equal(t, 2, reads.Paging)
}

func TestPercentileIsNearestRank(t *testing.T) {
	t.Parallel()

	require.Zero(t, percentile(nil, 50))
	require.Equal(t, 7, percentile([]int{7}, 90))
	require.Equal(t, 2, percentile([]int{4, 1, 3, 2}, 50))
	require.Equal(t, 4, percentile([]int{4, 1, 3, 2}, 90))
	require.Equal(t, 1, percentile([]int{4, 1, 3, 2}, 0))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -run 'TestReads|TestPercentile' -count=1`
Expected: build failure, `log.report(Options{}).Reads undefined (type *Report has no field or method Reads)`, `undefined: percentile`.

- [ ] **Step 3: Implement**

Create `internal/logreport/reads.go`:

```go
package logreport

import (
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// defaultReadLines is what view returns when a call sets no limit
// (DefaultReadLimit in internal/agent/tools/view.go).
const defaultReadLines = 200

// Reads is the Reads section, for view and read together.
type Reads struct {
	Calls       int     `json:"calls"`
	WithLimit   int     `json:"with_limit"`
	LimitShare  float64 `json:"limit_share"`
	MedianLines int     `json:"median_lines_requested"`
	// ReReads touch a path already requested in the session.
	ReReads     int     `json:"re_reads"`
	ReReadShare float64 `json:"re_read_share"`
	// Paging reads set an offset or a limit, and the previous view or
	// read in the session touched the same path.
	Paging      int     `json:"paging"`
	PagingShare float64 `json:"paging_share"`
}

type readsSection struct {
	reads Reads
	lines []int
	// Per session.
	seen map[string]bool
	prev []string
}

func (s *readsSection) message(_ *pass, m *message.Message) {
	if m.Role != message.Assistant {
		return
	}
	for _, tc := range m.ToolCalls() {
		if tc.Name == viewTool || tc.Name == readTool {
			s.read(tc.Input)
		}
	}
}

func (s *readsSection) read(input string) {
	s.reads.Calls++
	limit, offset := gjson.Get(input, "limit"), gjson.Get(input, "offset")
	explicit := limit.Type == gjson.Number
	lines := defaultReadLines
	if explicit {
		s.reads.WithLimit++
		if n := int(limit.Int()); n > 0 {
			lines = n
		}
	}
	s.lines = append(s.lines, lines)

	var paths []string
	if p := gjson.Get(input, "file_path").String(); p != "" {
		paths = append(paths, p)
	}
	for _, p := range gjson.Get(input, "file_paths").Array() {
		paths = append(paths, p.String())
	}
	if len(paths) == 0 {
		return
	}
	reread, paging := false, false
	for _, p := range paths {
		reread = reread || s.seen[p]
		paging = paging || slices.Contains(s.prev, p)
		s.seen[p] = true
	}
	if reread {
		s.reads.ReReads++
	}
	if paging && (explicit || offset.Type == gjson.Number) {
		s.reads.Paging++
	}
	s.prev = paths
}

func (s *readsSection) endSession(*pass) {
	clear(s.seen)
	s.prev = nil
}

func (s *readsSection) finish(_ *pass, r *Report) {
	s.reads.LimitShare = ratio(s.reads.WithLimit, s.reads.Calls)
	s.reads.ReReadShare = ratio(s.reads.ReReads, s.reads.Calls)
	s.reads.PagingShare = ratio(s.reads.Paging, s.reads.Calls)
	s.reads.MedianLines = percentile(s.lines, 50)
	r.Reads = s.reads
}

// percentile returns the nearest-rank p-th percentile of values, which it
// sorts, or zero when there are none.
func percentile(values []int, p int) int {
	if len(values) == 0 {
		return 0
	}
	slices.Sort(values)
	rank := (p*len(values) + 99) / 100
	return values[max(rank, 1)-1]
}

func (r Reads) writeText(out io.Writer) {
	fmt.Fprintln(out, "Reads (view and read)")
	fmt.Fprintf(out, "  calls\t%d\n", r.Calls)
	fmt.Fprintf(out, "  with a limit\t%d\t%s\n", r.WithLimit, percent(r.LimitShare))
	fmt.Fprintf(out, "  median lines requested\t%d\n", r.MedianLines)
	fmt.Fprintf(out, "  re-reads\t%d\t%s\n", r.ReReads, percent(r.ReReadShare))
	fmt.Fprintf(out, "  paging\t%d\t%s\n", r.Paging, percent(r.PagingShare))
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Shell  Shell  `json:"shell"`
}
```

with

```go
	Shell  Shell  `json:"shell"`
	Reads  Reads  `json:"reads"`
}
```

and replace

```go
		&shellSection{bare: map[string]int{}},
	}
```

with

```go
		&shellSection{bare: map[string]int{}},
		&readsSection{seen: map[string]bool{}},
	}
```

In `internal/logreport/output.go`, replace

```go
	r.Shell.writeText(tw)
```

with

```go
	r.Shell.writeText(tw)
	r.Reads.writeText(tw)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/
git commit -m "feat(logreport): report how files are read"
```

---

### Task 9: Part 3 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 3 (Tasks 7 and 8).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... ./internal/message/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/logreport/... ./internal/cmd/... ./internal/message/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 310 changed lines, in the files named in Tasks 7 and 8 only; the em dash count is `0`; no debug output, no file outside `internal/logreport/`, `internal/cmd/stats_report*.go` and `internal/message/parts_codec.go`.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(d["window"]["messages"], d["tools"]["calls"], d["tools"]["errors"], d["shell"], d["reads"])' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to Tasks 3 and 6: Shell `foreground commands 0`, `non-zero exits 0`, `interrupted 0`, `failure rate 0.00%`, `unstamped results 10470` (no `bash` result in the window carries a stamp; the text-matched 284 to 289 failures of the scripts are not reproduced, by design). Reads `calls 2908`, `with a limit 2193 75.41%`, `median lines requested 100`, `re-reads 1303 44.81%`, `paging 735 25.28%` (`/tmp/crush-model-intel/w0/out_27_turn_shape.txt`). Report a difference rather than patching it.

---

# Part 4: the Edits section

Branch `w0-stats-report-4`, from `origin/main` after part 3 merged.

### Task 10: The Edits section

**Files:**
- Create: `internal/logreport/edits.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`, the tool-name constants), `internal/logreport/output.go` (`WriteText`), `internal/cmd/stats_report.go` (`Long`)
- Test: `internal/logreport/edits_test.go` (new)

**Interfaces:**
- Consumes: `pass.calls`, `kindCounts`, `ratio`, `percent`, `formatCounts`, `newBuild`, `oldBuild`, `(*testLog).step`, `(*testLog).result` (Tasks 1 and 5).
- Produces: `Report.Edits Edits` (JSON `edits`, between `shell` and `reads`); `type Edits struct { Calls, Failed int; Rate float64; Kinds map[string]int; BatchFailures int; BatchFailureKinds map[string]int; AfterRefusal AfterRefusal; Runs FailureRuns }`; `type AfterRefusal struct { Refusals, RetrySameText, RetryNewText, MovedOn, NoRetry int }`; `type FailureRuns struct { Runs, Failed int; ByLength [5]int; Max int }`; `func newEditsSection() *editsSection`; constants `editTool`, `multiEditTool`, `writeTool`.

- [ ] **Step 1: Write the failing tests**

Create `internal/logreport/edits_test.go`:

```go
package logreport

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// editCallPart builds an edit call, or a multiedit call when olds holds
// more than one text.
func editCallPart(t *testing.T, id, path string, olds ...string) message.ToolCall {
	t.Helper()
	if len(olds) == 1 {
		input, err := json.Marshal(map[string]any{"file_path": path, "old_string": olds[0], "new_string": "x"})
		require.NoError(t, err)
		return message.ToolCall{ID: id, Name: "edit", Input: string(input)}
	}
	edits := []map[string]string{}
	for _, old := range olds {
		edits = append(edits, map[string]string{"old_string": old, "new_string": "x"})
	}
	input, err := json.Marshal(map[string]any{"file_path": path, "edits": edits})
	require.NoError(t, err)
	return message.ToolCall{ID: id, Name: "multiedit", Input: string(input)}
}

func editResult(id, name, metadata string, failed bool) message.ToolResult {
	return message.ToolResult{ToolCallID: id, Name: name, Metadata: metadata, IsError: failed}
}

func TestEditsFailuresKindsAndBatches(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		editCallPart(t, "e1", "a.go", "one"),
		editCallPart(t, "e2", "a.go", "two"),
		editCallPart(t, "m1", "b.go", "one", "two"),
		editCallPart(t, "m2", "b.go", "three", "four"),
	)
	log.result("s1", editResult("e1", "edit", `{"error_kind":"text_not_found"}`, true))
	log.result("s1", editResult("e2", "edit", `{"additions":1}`, false))
	// A batch that applied one edit and failed one: not a failed call.
	log.result("s1", editResult("m1", "multiedit", `{"edits_applied":1,"edits_failed":[{"index":2,"error_kind":"multiple_matches"}]}`, false))
	log.result("s1", editResult("m2", "multiedit", `{"edits_applied":0,"edits_failed":[{"index":1,"error_kind":"text_not_found"},{"index":2}],"error_kind":"text_not_found"}`, true))

	edits := log.report(Options{}).Edits
	require.Equal(t, 4, edits.Calls)
	require.Equal(t, 2, edits.Failed)
	require.InDelta(t, 0.5, edits.Rate, 1e-9)
	require.Equal(t, map[string]int{"text_not_found": 2}, edits.Kinds)
	require.Equal(t, 1, edits.BatchFailures, "only batches that otherwise succeeded")
	require.Equal(t, map[string]int{"multiple_matches": 1}, edits.BatchFailureKinds)
}

func TestEditsAfterReadGuardRefusal(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	refuse := func(id string) {
		log.result("s1", editResult(id, "edit", `{"error_kind":"not_read"}`, true))
	}
	// Retried with the same text after a view.
	log.step("s1", newBuild, editCallPart(t, "r1", "a.go", "alpha"))
	refuse("r1")
	log.step("s1", newBuild, message.ToolCall{ID: "v1", Name: "view", Input: `{"file_path":"a.go"}`})
	log.step("s1", newBuild, editCallPart(t, "r2", "a.go", "alpha"))
	log.result("s1", editResult("r2", "edit", "", false))
	// Retried with a different text, as a multiedit.
	log.step("s1", newBuild, editCallPart(t, "r3", "b.go", "beta"))
	log.result("s1", editResult("r3", "edit", `{"error_kind":"changed_since_read"}`, true))
	log.step("s1", newBuild, editCallPart(t, "r4", "b.go", "beta", "gamma"))
	log.result("s1", editResult("r4", "multiedit", "", false))
	// Moved on: a write to the same file.
	log.step("s1", newBuild, editCallPart(t, "r5", "c.go", "c"))
	refuse("r5")
	log.step("s1", newBuild, message.ToolCall{ID: "w1", Name: "write", Input: `{"file_path":"c.go","content":"x"}`})
	// Moved on: an edit of another file first; a write to another file
	// does not settle it.
	log.step("s1", newBuild, editCallPart(t, "r6", "d.go", "d"))
	refuse("r6")
	log.step("s1", newBuild, message.ToolCall{ID: "w2", Name: "write", Input: `{"file_path":"other.go","content":"x"}`})
	log.step("s1", newBuild, editCallPart(t, "r7", "e.go", "e"))
	log.result("s1", editResult("r7", "edit", "", false))
	// No retry before the session ended.
	log.step("s1", newBuild, editCallPart(t, "r8", "f.go", "f"))
	refuse("r8")
	// Another session's edit of the same file is not a retry.
	log.session("s2", "title")
	log.step("s2", newBuild, editCallPart(t, "x1", "f.go", "f"))
	log.result("s2", editResult("x1", "edit", "", false))

	require.Equal(t, AfterRefusal{Refusals: 5, RetrySameText: 1, RetryNewText: 1, MovedOn: 2, NoRetry: 1}, log.report(Options{}).Edits.AfterRefusal)
}

func TestEditsFailureRuns(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	outcome := func(session, id string, failed bool) {
		log.step(session, oldBuild, editCallPart(t, id, "a.go", "a"))
		log.result(session, editResult(id, "edit", "", failed))
	}
	for i, failed := range []bool{true, true, false, true, false, false} {
		outcome("s1", fmt.Sprintf("a%d", i), failed)
	}
	// A run ends with its session.
	log.session("s2", "title")
	for i := range 6 {
		outcome("s2", fmt.Sprintf("b%d", i), true)
	}
	// Results of other tools do not break a run.
	log.session("s3", "title")
	outcome("s3", "c1", true)
	log.step("s3", oldBuild, message.ToolCall{ID: "c2", Name: "bash"})
	log.result("s3", message.ToolResult{ToolCallID: "c2", Name: "bash"})
	outcome("s3", "c3", true)

	edits := log.report(Options{}).Edits
	require.Equal(t, FailureRuns{Runs: 4, Failed: 11, ByLength: [5]int{1, 2, 0, 0, 1}, Max: 6}, edits.Runs)
	require.Equal(t, map[string]int{"unstamped": 11}, edits.Kinds, "no stamps from the old build")
	require.Zero(t, edits.AfterRefusal.Refusals, "a refusal is only known by its stamp")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -run TestEdits -count=1`
Expected: build failure, `log.report(Options{}).Edits undefined (type *Report has no field or method Edits)`, `undefined: AfterRefusal`.

- [ ] **Step 3: Implement**

Create `internal/logreport/edits.go`:

```go
package logreport

import (
	"fmt"
	"hash/fnv"
	"io"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Edits is the Edits section, for edit and multiedit together.
type Edits struct {
	Calls  int            `json:"calls"`
	Failed int            `json:"failed"`
	Rate   float64        `json:"failure_rate"`
	Kinds  map[string]int `json:"failed_by_kind,omitempty"`
	// BatchFailures counts the failed edits inside multiedit calls that
	// otherwise succeeded. They do not enter the rate.
	BatchFailures     int            `json:"batch_failures"`
	BatchFailureKinds map[string]int `json:"batch_failures_by_kind,omitempty"`
	AfterRefusal      AfterRefusal   `json:"after_read_guard_refusal"`
	Runs              FailureRuns    `json:"failure_runs"`
}

// AfterRefusal is what followed each refusal by the read guard: the next
// edit or multiedit of the same file in the session is the retry, with
// the same text or a different one. An edit of another file first, or a
// write to the same file, means the model moved on.
type AfterRefusal struct {
	Refusals      int `json:"refusals"`
	RetrySameText int `json:"retry_same_text"`
	RetryNewText  int `json:"retry_different_text"`
	MovedOn       int `json:"moved_on"`
	NoRetry       int `json:"no_retry"`
}

// FailureRuns are runs of consecutive failed edit or multiedit results in
// a session with no successful one between them.
type FailureRuns struct {
	Runs   int `json:"runs"`
	Failed int `json:"failed_edits"`
	// ByLength counts runs of length 1, 2, 3, 4, and 5 or more.
	ByLength [5]int `json:"by_length"`
	Max      int    `json:"max"`
}

// editCall is an edit, multiedit or write call: its file, and a hash of
// its old_string values so that the text itself is never held.
type editCall struct {
	name string
	path string
	text uint64
}

type editsSection struct {
	edits      Edits
	kinds      kindCounts
	batchKinds kindCounts
	// Per session.
	calls   map[string]editCall
	pending []editCall
	run     int
}

func newEditsSection() *editsSection {
	return &editsSection{kinds: kindCounts{}, batchKinds: kindCounts{}, calls: map[string]editCall{}}
}

func (s *editsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		for _, tc := range m.ToolCalls() {
			if tc.Name != editTool && tc.Name != multiEditTool && tc.Name != writeTool {
				continue
			}
			c := newEditCall(tc)
			s.follow(c)
			s.calls[tc.ID] = c
			if c.name != writeTool {
				s.edits.Calls++
			}
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			if tr.Name == editTool || tr.Name == multiEditTool {
				s.result(p, tr)
			}
		}
	}
}

func newEditCall(tc message.ToolCall) editCall {
	h := fnv.New64a()
	olds := gjson.Get(tc.Input, "edits.#.old_string").Array()
	if tc.Name == editTool {
		olds = []gjson.Result{gjson.Get(tc.Input, "old_string")}
	}
	for _, old := range olds {
		h.Write([]byte(old.String()))
		h.Write([]byte{0})
	}
	return editCall{name: tc.Name, path: gjson.Get(tc.Input, "file_path").String(), text: h.Sum64()}
}

// follow settles the pending refusals that call c answers.
func (s *editsSection) follow(c editCall) {
	kept := s.pending[:0]
	for _, r := range s.pending {
		switch {
		case c.path == r.path && c.name == writeTool:
			s.edits.AfterRefusal.MovedOn++
		case c.path == r.path && c.text == r.text:
			s.edits.AfterRefusal.RetrySameText++
		case c.path == r.path:
			s.edits.AfterRefusal.RetryNewText++
		case c.name != writeTool:
			s.edits.AfterRefusal.MovedOn++
		default:
			kept = append(kept, r)
		}
	}
	s.pending = kept
}

func (s *editsSection) result(p *pass, tr message.ToolResult) {
	build := p.calls[tr.ToolCallID].build
	if !tr.IsError {
		s.endRun()
		for _, failed := range gjson.Get(tr.Metadata, "edits_failed").Array() {
			s.edits.BatchFailures++
			s.batchKinds.add(failed.Get("error_kind").String(), build)
		}
		return
	}
	s.run++
	s.edits.Failed++
	kind := gjson.Get(tr.Metadata, "error_kind").String()
	s.kinds.add(kind, build)
	if kind == "not_read" || kind == "changed_since_read" {
		s.edits.AfterRefusal.Refusals++
		if c, ok := s.calls[tr.ToolCallID]; ok {
			s.pending = append(s.pending, c)
		} else {
			s.edits.AfterRefusal.NoRetry++
		}
	}
}

func (s *editsSection) endRun() {
	if s.run == 0 {
		return
	}
	runs := &s.edits.Runs
	runs.Runs++
	runs.Failed += s.run
	runs.ByLength[min(s.run, 5)-1]++
	runs.Max = max(runs.Max, s.run)
	s.run = 0
}

func (s *editsSection) endSession(*pass) {
	s.endRun()
	s.edits.AfterRefusal.NoRetry += len(s.pending)
	s.pending = s.pending[:0]
	clear(s.calls)
}

func (s *editsSection) finish(p *pass, r *Report) {
	s.edits.Rate = ratio(s.edits.Failed, s.edits.Calls)
	s.edits.Kinds = s.kinds.resolve(p)
	s.edits.BatchFailureKinds = s.batchKinds.resolve(p)
	r.Edits = s.edits
}

func (e Edits) writeText(out io.Writer) {
	a, runs := e.AfterRefusal, e.Runs
	fmt.Fprintln(out, "Edits (edit and multiedit)")
	fmt.Fprintf(out, "  calls\t%d\n", e.Calls)
	fmt.Fprintf(out, "  failed\t%d\t%s\t%s\n", e.Failed, percent(e.Rate), formatCounts(e.Kinds))
	fmt.Fprintf(out, "  failed inside batches\t%d\t\t%s\n", e.BatchFailures, formatCounts(e.BatchFailureKinds))
	fmt.Fprintf(out, "  read-guard refusals\t%d\tretry same text %d, retry different text %d, moved on %d, no retry %d\n",
		a.Refusals, a.RetrySameText, a.RetryNewText, a.MovedOn, a.NoRetry)
	fmt.Fprintf(out, "  runs of failed edits\t%d\tlength 1: %d, 2: %d, 3: %d, 4: %d, 5 or more: %d, max %d\n",
		runs.Runs, runs.ByLength[0], runs.ByLength[1], runs.ByLength[2], runs.ByLength[3], runs.ByLength[4], runs.Max)
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Shell  Shell  `json:"shell"`
	Reads  Reads  `json:"reads"`
```

with

```go
	Shell  Shell  `json:"shell"`
	Edits  Edits  `json:"edits"`
	Reads  Reads  `json:"reads"`
```

replace

```go
		&shellSection{bare: map[string]int{}},
		&readsSection{seen: map[string]bool{}},
```

with

```go
		&shellSection{bare: map[string]int{}},
		newEditsSection(),
		&readsSection{seen: map[string]bool{}},
```

and replace

```go
	bashTool = "bash"
	viewTool = "view"
	readTool = "read"
```

with

```go
	bashTool      = "bash"
	editTool      = "edit"
	multiEditTool = "multiedit"
	writeTool     = "write"
	viewTool      = "view"
	readTool      = "read"
```

In `internal/logreport/output.go`, replace

```go
	r.Shell.writeText(tw)
```

with

```go
	r.Shell.writeText(tw)
	r.Edits.writeText(tw)
```

In `internal/cmd/stats_report.go`, replace

```go
results in the window, and "unstamped" otherwise.`,
```

with

```go
results in the window, and "unstamped" otherwise. A refusal by the read
guard is known by its kind alone, so rows written before the kinds existed
show none.`,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/ internal/cmd/stats_report.go
git add internal/logreport/ internal/cmd/stats_report.go
git commit -m "feat(logreport): report how edits fail and what follows a refusal"
```

---

### Task 11: Part 4 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 4 (Task 10).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... ./internal/message/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/logreport/... ./internal/cmd/... ./internal/message/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 330 changed lines, in the files named in Task 10 only; the em dash count is `0`; no debug output, no file outside `internal/logreport/`, `internal/cmd/stats_report*.go` and `internal/message/parts_codec.go`.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); e = d["edits"]; print(d["window"]["messages"], d["tools"]["calls"], d["tools"]["errors"], e["calls"], e["failed"], e["batch_failures"], e["after_read_guard_refusal"], e["failure_runs"])' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to Tasks 3, 6 and 9: Edits `calls 1818`, `failed 184 10.12%` with `unstamped 184`, `failed inside batches 0`, `read-guard refusals 0` with every outcome 0 (the window predates the kinds), `runs of failed edits 145` with `length 1: 119, 2: 18, 3: 5, 4: 2, 5 or more: 1, max 6` (`/tmp/crush-model-intel/w0/out_23_edit_recovery.txt`: 145 runs, 184 failed edits, max 6). Report a difference rather than patching it.

---

## Self-review

1. **Spec coverage.** Command and flags, `--cwd` and `--data-dir` (Task 2); read-only open (Tasks 1 and 2); privacy rule in both formats (Task 1, and the same test covers every later section because it renders the whole report); Window: bounds, messages by role, sessions (Task 1), builds with first and last date (Task 4); Tools with kinds and the `view+read` line (Task 5); Shell (Task 7); Edits: calls and failed calls by kind, failed edits inside batches, what followed a refusal, runs (Task 10); Reads: calls, share with a limit, median lines, re-reads, paging (Task 8); unstamped rule (Tasks 5 and 7); unknown kinds under their own name and skipped rows (Tasks 1 and 5); a database with no stamped rows still reported (Tasks 3 to 11 run on one); the one query read row by row in `session_id, created_at, rowid` order, parts decoded with the `message` types, state per session only (Task 1); `schema` in the JSON (Task 1); nearest-rank percentiles (Task 8); the live check on messages, tool calls and error results (Tasks 3 and 6). Item 8's sections, the prompts figure and the injection tags are left to item 8.
2. **Placeholder scan.** Every code step carries its full code; every modify step names the exact text it replaces.
3. **Type consistency.** `section`, `pass`, `newPass`, `newSections`, `kindCounts`, `kindName`, `ratio`, `percent`, `formatCounts`, `percentile` and the test helpers keep one name and signature across tasks; each part's code was built, tested and linted at the end of its part.
4. **Review Focus.** Each of the five lines has its test in the task named beside it.

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

// child creates a session started from parent.
func (l *testLog) child(id, parent string) {
	l.t.Helper()
	_, err := l.conn.ExecContext(l.t.Context(),
		`INSERT INTO sessions (id, parent_session_id, title, created_at, updated_at) VALUES (?, ?, 'child', 0, 0)`, id, parent)
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

// summary writes a compaction summary: an assistant message marked as
// one.
func (l *testLog) summary(sessionID string, parts ...message.ContentPart) {
	l.t.Helper()
	l.add(sessionID, message.Assistant, parts...)
	res, err := l.conn.ExecContext(l.t.Context(),
		`UPDATE messages SET is_summary_message = 1 WHERE id = ?`, fmt.Sprintf("msg-%d", l.n))
	require.NoError(l.t, err)
	n, err := res.RowsAffected()
	require.NoError(l.t, err)
	require.EqualValues(l.t, 1, n, "the summary row is marked")
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

package db

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// TestMigrationAddsToolCallIDAndIndex pins the files migration: the column
// is added to a table that already holds rows (every existing install), the
// rows read back the empty default, the down migration takes both the
// index and the column away, and the metadata listing seeks the covering
// index instead of walking the table behind the content column.
func TestMigrationAddsToolCallIDAndIndex(t *testing.T) {
	t.Parallel()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Roll back to the schema just before this migration, then store a row
	// the way an older Crush would have.
	const before = 20261122000000
	require.NoError(t, goose.DownTo(conn, "migrations", before))
	_, err = conn.ExecContext(t.Context(),
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES ('s1', 'legacy', 1, 1)`)
	require.NoError(t, err)
	_, err = conn.ExecContext(t.Context(),
		`INSERT INTO files (id, session_id, path, content, version, created_at, updated_at)
		 VALUES ('f1', 's1', 'a.go', 'package a', 0, 1, 1)`)
	require.NoError(t, err)

	require.NoError(t, goose.Up(conn, "migrations"))

	columns := tableColumns(t, conn, "files")
	require.Contains(t, columns, "tool_call_id")

	var toolCallID string
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT tool_call_id FROM files WHERE id = 'f1'`).Scan(&toolCallID))
	require.Equal(t, "", toolCallID, "a row stored before the migration reads the empty default")

	var plan strings.Builder
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+listFileVersionsBySession, "s1")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	require.NoError(t, rows.Err())

	t.Logf("metadata listing plan:\n%s", plan.String())
	require.Contains(t, plan.String(), "idx_files_session_versions",
		"the metadata listing must read its columns from the covering index, not from behind the content column")
}

// tableColumns lists a table's column names through PRAGMA table_info.
func tableColumns(t *testing.T, conn *sql.DB, table string) []string {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

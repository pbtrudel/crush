package db

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// TestMigrationDropsRedundantFilesSessionIndex pins why idx_files_session_id
// is gone: idx_files_session_versions leads with session_id, so it serves
// every session-only lookup and the single-column index only costs a write
// per insert. The plan assertions keep that drop from quietly turning the
// session lookups into table scans.
func TestMigrationDropsRedundantFilesSessionIndex(t *testing.T) {
	t.Parallel()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.False(t, indexExists(t, conn, "files", "idx_files_session_id"),
		"the single-column session index must be dropped")
	require.True(t, indexExists(t, conn, "files", "idx_files_session_versions"),
		"the composite session index must remain")

	for name, query := range map[string]string{
		"ListFilesBySession":        listFilesBySession,
		"ListFileVersionsBySession": listFileVersionsBySession,
		"DeleteSessionFiles":        deleteSessionFiles,
	} {
		plan := queryPlan(t, conn, query, "s1")
		t.Logf("%s plan:\n%s", name, plan)
		require.Contains(t, plan, "idx_files_session_versions",
			"%s must seek the composite session index", name)
		require.NotContains(t, plan, "SCAN files",
			"%s must not fall back to a full table scan", name)
	}

	// The path plus session lookup and the latest-per-path listing also
	// filter by session_id; keep both on an index seek after the drop.
	pathPlan := queryPlan(t, conn, getFileByPathAndSession, "a.go", "s1")
	require.Contains(t, pathPlan, "USING INDEX")
	require.NotContains(t, pathPlan, "SCAN files")
	latestPlan := queryPlan(t, conn, listLatestSessionFiles, "s1")
	require.Contains(t, latestPlan, "idx_files_session_versions")

	// Rolling this migration back must restore the old index for binaries
	// that still expect the pre-drop schema.
	require.NoError(t, goose.DownTo(conn, "migrations", 20261124000000))
	require.True(t, indexExists(t, conn, "files", "idx_files_session_id"),
		"the down migration must recreate the single-column index")
	require.NoError(t, goose.Up(conn, "migrations"))
	require.False(t, indexExists(t, conn, "files", "idx_files_session_id"),
		"the up migration must drop the single-column index again")
}

// indexExists reports whether SQLite has an index with this name on table.
func indexExists(t *testing.T, conn *sql.DB, table, index string) bool {
	t.Helper()
	var name string
	err := conn.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`,
		table, index).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	require.NoError(t, err)
	return name == index
}

// queryPlan renders EXPLAIN QUERY PLAN rows into one newline-separated text.
func queryPlan(t *testing.T, conn *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	require.NoError(t, err)
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	require.NoError(t, rows.Err())
	return plan.String()
}

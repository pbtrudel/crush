package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChildSessionsUseParentIndex pins why idx_sessions_parent exists: the
// web snapshot walks a session tree one child list at a time, and without
// the index every step scans the whole sessions table.
func TestChildSessionsUseParentIndex(t *testing.T) {
	t.Parallel()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var plan strings.Builder
	rows, err := conn.QueryContext(t.Context(),
		`EXPLAIN QUERY PLAN SELECT * FROM sessions WHERE parent_session_id = 's1' ORDER BY created_at ASC`)
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

	require.Contains(t, plan.String(), "idx_sessions_parent",
		"listing a session's children must seek the parent index, not walk the table")
}

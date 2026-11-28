package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// messagesPlan returns the query plan of q as one string per line.
func messagesPlan(t *testing.T, q string, args ...any) string {
	t.Helper()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+q, args...)
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

// TestMessagesPageUsesSessionIndex pins why idx_messages_session_created_at
// exists: the gateway's paging reads one page of a session, and without the
// index every page sorts the whole session.
func TestMessagesPageUsesSessionIndex(t *testing.T) {
	t.Parallel()

	for name, q := range map[string]string{
		"newest page": `SELECT * FROM messages
			WHERE session_id = 's1'
			ORDER BY created_at DESC, rowid DESC
			LIMIT 21`,
		"page before": `SELECT m.* FROM messages m
			WHERE m.session_id = 's1'
			  AND (m.created_at, m.rowid) < (SELECT b.created_at, b.rowid FROM messages b WHERE b.id = 'm1')
			ORDER BY m.created_at DESC, m.rowid DESC
			LIMIT 21`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := messagesPlan(t, q)
			require.Contains(t, got, "idx_messages_session_created_at",
				"a page must seek the session index, not scan the messages table")
			require.NotContains(t, got, "TEMP B-TREE",
				"the index covers the ordering, so no sort should be needed")
			require.NotContains(t, got, "idx_messages_session_id",
				"the single-column index is gone; the composite one serves every session lookup")
		})
	}
}

// TestFullSessionListKeepsItsOrder pins that the whole-transcript read the
// settled path still uses keeps its exact order and needs no sort: the
// session index covers created_at, and rowid breaks same-second ties.
func TestFullSessionListKeepsItsOrder(t *testing.T) {
	t.Parallel()

	got := messagesPlan(t, `SELECT * FROM messages
		WHERE session_id = 's1'
		ORDER BY created_at ASC, rowid ASC`)
	require.Contains(t, got, "idx_messages_session_created_at")
	require.NotContains(t, got, "TEMP B-TREE")
}

// TestSettleReadsUseSessionIndex pins the two queries a settle pass runs. They
// walk the same session in the same order as the pages they precede, so the
// composite index has to serve both.
func TestSettleReadsUseSessionIndex(t *testing.T) {
	t.Parallel()

	for name, q := range map[string]string{
		"unfinished assistants": `SELECT * FROM messages
			WHERE session_id = 's1'
			  AND role = 'assistant'
			  AND is_summary_message = 0
			  AND finished_at IS NULL
			ORDER BY created_at ASC, rowid ASC`,
		"tool rows from the bound": `SELECT * FROM messages
			WHERE session_id = 's1'
			  AND role = 'tool'
			  AND created_at >= 5
			ORDER BY created_at ASC, rowid ASC`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := messagesPlan(t, q)
			require.Contains(t, got, "idx_messages_session_created_at",
				"a settle read must seek the session index, not walk the table")
			require.NotContains(t, got, "TEMP B-TREE",
				"the index covers the ordering, so no sort should be needed")
		})
	}
}

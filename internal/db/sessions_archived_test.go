package db

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A scripted run keeps its session out of the lists by archiving it. A row
// written the way an older schema wrote it reads as not archived, the list
// leaves the archived ones out, and fetching one by id still finds it.
func TestArchivedSessionsLeaveTheList(t *testing.T) {
	t.Parallel()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := New(conn)
	ctx := t.Context()

	_, err = conn.ExecContext(ctx, `INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at)
		VALUES ('old', 'Old', 0, 0, 0, 0, 1, 1)`)
	require.NoError(t, err)
	old, err := q.GetSessionByID(ctx, "old")
	require.NoError(t, err)
	require.Equal(t, int64(0), old.Archived, "the migration leaves rows written before it unarchived")

	scripted, err := q.CreateSession(ctx, CreateSessionParams{ID: uuid.New().String(), Title: "Scripted"})
	require.NoError(t, err)
	require.Equal(t, int64(0), scripted.Archived, "a session is not archived unless it says so")
	archived, err := q.SetSessionArchived(ctx, SetSessionArchivedParams{ID: scripted.ID, Archived: 1})
	require.NoError(t, err)
	require.Equal(t, int64(1), archived.Archived)

	list, err := q.ListSessions(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "old", list[0].ID)

	byID, err := q.GetSessionByID(ctx, scripted.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), byID.Archived, "the session is still readable by id")
}

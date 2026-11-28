package message

import (
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// newPagingService returns a message service and two sessions of the same
// store, so a test can also page across sessions.
func newPagingService(t *testing.T) (Service, string, string) {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	sessions := session.NewService(q, conn)
	first, err := sessions.Create(t.Context(), "first")
	require.NoError(t, err)
	second, err := sessions.Create(t.Context(), "second")
	require.NoError(t, err)
	return NewService(q), first.ID, second.ID
}

// createMessages stores n messages and returns their ids oldest first.
func createMessages(t *testing.T, svc Service, sessionID string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for range n {
		msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}
	return ids
}

// messageIDs reports the ids of msgs, in the order they were returned.
func messageIDs(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// TestPageMessagesWalksBackwards pins the page boundaries: the newest page
// first, then older pages ending before the id the client already has, the
// same slices the gateway used to take from the whole session.
func TestPageMessagesWalksBackwards(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	ids := createMessages(t, svc, sessionID, 5)

	page, hasMore, err := svc.PageMessages(t.Context(), sessionID, "", 2)
	require.NoError(t, err)
	require.Equal(t, ids[3:], messageIDs(page), "the newest page, oldest first")
	require.True(t, hasMore)

	page, hasMore, err = svc.PageMessages(t.Context(), sessionID, ids[3], 2)
	require.NoError(t, err)
	require.Equal(t, ids[1:3], messageIDs(page))
	require.True(t, hasMore)

	page, hasMore, err = svc.PageMessages(t.Context(), sessionID, ids[1], 2)
	require.NoError(t, err)
	require.Equal(t, ids[:1], messageIDs(page))
	require.False(t, hasMore)

	page, hasMore, err = svc.PageMessages(t.Context(), sessionID, ids[0], 2)
	require.NoError(t, err)
	require.Empty(t, page)
	require.False(t, hasMore)
}

// TestPageMessagesRefusesAnUnknownID pins the refusal: an id the session does
// not have is an error, never a silent jump to the newest page. An id that
// belongs to another session counts too.
func TestPageMessagesRefusesAnUnknownID(t *testing.T) {
	t.Parallel()

	svc, sessionID, otherSessionID := newPagingService(t)
	createMessages(t, svc, sessionID, 2)
	other, err := svc.Create(t.Context(), otherSessionID, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "elsewhere"}},
	})
	require.NoError(t, err)

	for _, before := range []string{"nope", other.ID} {
		_, _, err = svc.PageMessages(t.Context(), sessionID, before, 10)
		require.ErrorIs(t, err, ErrUnknownMessage, before)
	}
}

// TestPageMessagesReadsOnlyThePage pins the point of the query: it returns at
// most the limit it was asked for, so a whole transcript is never loaded.
func TestPageMessagesReadsOnlyThePage(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	createMessages(t, svc, sessionID, 500)

	page, hasMore, err := svc.PageMessages(t.Context(), sessionID, "", 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.True(t, hasMore)
}

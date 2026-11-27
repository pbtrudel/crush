package history_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// newHistoryTestService opens a fresh database, creates one session and
// returns a history service for it.
func newHistoryTestService(t *testing.T) (history.Service, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool: parallel tests share the global pool.
		require.NoError(t, db.Release(dataDir))
	})
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	q := db.New(conn)
	sess, err := session.NewService(q, conn).Create(t.Context(), "history meta")
	require.NoError(t, err)

	return history.NewService(q, conn), sess.ID
}

// TestListMetaBySessionOmitsContent pins that the metadata query counts
// bytes rather than characters and still leaves the full version
// available by id.
func TestListMetaBySessionOmitsContent(t *testing.T) {
	t.Parallel()
	svc, sessionID := newHistoryTestService(t)
	big := strings.Repeat("é", 10) // 20 bytes, 10 characters.
	_, err := svc.Create(t.Context(), sessionID, "/repo/a.go", big)
	require.NoError(t, err)
	_, err = svc.CreateVersion(t.Context(), sessionID, "/repo/a.go", big+"x")
	require.NoError(t, err)

	metas, err := svc.ListMetaBySession(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, metas, 2)
	require.Equal(t, int64(20), metas[0].Size, "size counts bytes, not characters")
	require.Equal(t, int64(21), metas[1].Size)
	require.Equal(t, []int64{0, 1}, []int64{metas[0].Version, metas[1].Version})
	require.Equal(t, "/repo/a.go", metas[0].Path)
	require.Equal(t, sessionID, metas[0].SessionID)
	require.NotEmpty(t, metas[0].ID)

	full, err := svc.Get(t.Context(), metas[1].ID)
	require.NoError(t, err)
	require.Equal(t, big+"x", full.Content)
}

// TestListMetaBySessionUnknownSession pins the empty answer for a
// session with no history.
func TestListMetaBySessionUnknownSession(t *testing.T) {
	t.Parallel()
	svc, _ := newHistoryTestService(t)

	metas, err := svc.ListMetaBySession(t.Context(), "no-such-session")
	require.NoError(t, err)
	require.Empty(t, metas)
}

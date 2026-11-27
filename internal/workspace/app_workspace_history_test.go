package workspace

import (
	"database/sql"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/stretchr/testify/require"
)

// TestAppWorkspaceGetHistoryVersionMissing pins the in-process half of the
// documented contract: a version the store does not have wraps
// sql.ErrNoRows, while client mode answers client.ErrNotFound.
func TestAppWorkspaceGetHistoryVersionMissing(t *testing.T) {
	t.Parallel()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	a.History = history.NewService(db.New(conn), conn)

	w := NewAppWorkspace(a, nil)
	_, err = w.GetHistoryVersion(t.Context(), "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

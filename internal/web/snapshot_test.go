package web

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// TestSnapshotCountsRootSessions pins the count the HUD's pin toggle reads:
// only sessions with no parent count, whatever their state.
func TestSnapshotCountsRootSessions(t *testing.T) {
	t.Parallel()
	f := webtest.NewFake()
	f.AddSession(session.Session{ID: "sub-1", ParentSessionID: "s1", Title: "Map the parser"})
	f.AddSession(session.Session{ID: "team-m-1", ParentSessionID: "s1", Title: "Teammate alice"})
	g, err := Start(t.Context(), Options{Workspace: f})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })

	require.Equal(t, 1, g.buildSnapshot(t.Context(), "").RootSessions)

	f.AddSession(session.Session{ID: "s-2", Title: "Tokenizer port"})
	require.Equal(t, 2, g.buildSnapshot(t.Context(), "").RootSessions)
}

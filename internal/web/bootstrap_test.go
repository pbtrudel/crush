package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// TestBootstrapCarriesGlobalState pins the payload a shell renders before it
// has subscribed to anything.
func TestBootstrapCarriesGlobalState(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "s2", Title: "Second", UpdatedAt: 10})
	fake.SetSessionBusy("s2", true)
	fake.LSP = map[string]workspace.LSPClientInfo{
		"gopls": {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 2},
	}
	fake.MCP = map[string]mcp.ClientInfo{
		"fs": {Name: "fs", State: mcp.StateConnected, Counts: mcp.Counts{Tools: 3}},
	}
	// The pending store is filled directly: the pump goroutine owns the
	// event path and this test must not race it.
	g.pending.observe(questionEvent("b1", "s2"))

	b := g.buildBootstrap(t.Context())
	require.NotEmpty(t, b.Version)
	require.Equal(t, "/tmp/project", b.WorkingDir)
	require.Equal(t, "main", b.Branch)
	require.Equal(t, "s1", b.CurrentSessionID)
	require.Equal(t, "code", b.InputMode)
	require.True(t, b.AgentReady)
	require.Equal(t, ServerInfo{State: strconv.Itoa(int(lsp.StateReady)), DiagnosticCount: 2}, b.LSP["gopls"])
	require.Equal(t, ServerInfo{State: "connected", ToolCount: 3}, b.MCP["fs"])
	require.Len(t, b.Sessions, 2)
	statuses := map[string]string{}
	for _, row := range b.Sessions {
		statuses[row.ID] = row.Status
	}
	require.Equal(t, map[string]string{"s1": statusIdle, "s2": statusPendingInteraction}, statuses,
		"the open question outranks the busy run it waits on")

	// The terminal owns the current session, so a switch moves the
	// bootstrap with it.
	fake.SetCurrent("other")
	require.Equal(t, "other", bootstrapOf(t, g).CurrentSessionID)
}

// TestBootstrapRouteServesIt pins the endpoint the shell loads with.
func TestBootstrapRouteServesIt(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/bootstrap", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var b Bootstrap
	require.NoError(t, json.Unmarshal(body, &b))
	require.Equal(t, "s1", b.CurrentSessionID)
	require.Len(t, b.Sessions, 1)
	require.Equal(t, "s1", b.Sessions[0].ID)
	require.Equal(t, statusIdle, b.Sessions[0].Status)
}

// TestPayloadsHaveNoNullArrays pins the contract the frontend's zod schema
// reads: a workspace with no runs, no roster and no interactions must still
// answer arrays, not nulls.
func TestPayloadsHaveNoNullArrays(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for name, v := range map[string]any{
		"bootstrap":       g.buildBootstrap(t.Context()),
		"sessionSnapshot": g.buildSessionSnapshot(t.Context(), "s1", "", messagePageSize),
	} {
		raw, err := json.Marshal(v)
		require.NoError(t, err, name)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields), name)
		for key, value := range fields {
			require.NotEqual(t, "null", string(value), "%s.%s is null", name, key)
		}
	}
}

// TestSessionSnapshotCarriesOneSession pins the per-session payload.
func TestSessionSnapshotCarriesOneSession(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for _, id := range []string{"m1", "m2"} {
		fake.AddMessage(textMessage(id, "s1", "hello "+id))
	}
	fake.SetSessionBusy("s1", true)
	fake.SetSessionQueued("s1", []string{"later"})
	fake.AddHistory(historyFile("h1", "s1", "/tmp/project/main.go", 1))

	s := g.buildSessionSnapshot(t.Context(), "s1", "", messagePageSize)
	require.Equal(t, "s1", s.SessionID)
	require.NotEmpty(t, s.Session)
	require.Len(t, s.Messages, 2)
	require.False(t, s.HasMore)
	require.Len(t, s.Files, 1)
	require.Equal(t, "/tmp/project/main.go", s.Files[0].Path)
	require.True(t, s.Busy)
	require.Equal(t, []string{"later"}, s.Queued)
}

// TestSessionSnapshotPagesAndFlagsMore pins the paging the load-older button
// reads.
func TestSessionSnapshotPagesAndFlagsMore(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for i := range 5 {
		fake.AddMessage(textMessage("m"+strconv.Itoa(i), "s1", "hello"))
	}
	s := g.buildSessionSnapshot(t.Context(), "s1", "", 2)
	require.Len(t, s.Messages, 2)
	require.True(t, s.HasMore)
}

// TestSessionSnapshotForAnUnknownSession pins the degraded answer: the
// shell shows an empty session instead of failing the subscription.
func TestSessionSnapshotForAnUnknownSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	s := g.buildSessionSnapshot(t.Context(), "nope", "", messagePageSize)
	require.Empty(t, s.Session)
	require.Empty(t, s.Messages)
	require.False(t, s.HasMore)
}

func historyFile(id, sessionID, path string, version int64) history.File {
	return history.File{ID: id, SessionID: sessionID, Path: path, Content: "package main", Version: version}
}

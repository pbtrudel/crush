package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/server"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// newLSPHarness serves the real API over a synthetic workspace whose
// app carries an LSP manager, so the diagnostics routes can be driven
// without starting language servers. It lives in the external test
// package because the client SDK imports the server package.
func newLSPHarness(t *testing.T) (*httptest.Server, *app.App, string) {
	t.Helper()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	a.LSPManager = lsp.NewManager(config.NewTestStore(&config.Config{}))

	srv := server.NewServer(nil, "tcp", "127.0.0.1:0")
	ws := &backend.Workspace{ID: "ws-lsp", Path: t.TempDir(), App: a}
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})
	backend.InsertWorkspaceForTest(srv.Backend(), ws)

	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs, a, ws.ID
}

// addLSPClient registers a transport-less LSP client that reports the
// given diagnostics, as a language server would after publishing them.
func addLSPClient(t *testing.T, a *app.App, name string, diags map[protocol.DocumentURI][]protocol.Diagnostic) {
	t.Helper()
	client := lsp.NewForTest(name)
	for uri, list := range diags {
		params, err := json.Marshal(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: list})
		require.NoError(t, err)
		lsp.HandleDiagnostics(client, params)
	}
	a.LSPManager.Clients().Set(name, client)
}

// TestLSPDiagnosticsByFileRoute pins the batch route's wire contract
// and its coexistence with the per-server route: the counts of every
// server reporting the same file add up, every severity is a snake_case
// field, and an empty workspace answers an empty object.
func TestLSPDiagnosticsByFileRoute(t *testing.T) {
	t.Parallel()
	hs, a, workspaceID := newLSPHarness(t)

	get := func(path string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, hs.URL+path, nil)
		require.NoError(t, err)
		resp, err := hs.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, resp.Header, body
	}

	// No server has published anything yet.
	status, header, body := get("/v1/workspaces/" + workspaceID + "/lsps/diagnostics/by-file")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, header.Get("Content-Type"), "application/json")
	require.JSONEq(t, `{}`, string(body))

	addLSPClient(t, a, "gopls", map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {
			{Severity: protocol.SeverityError},
			{Severity: protocol.SeverityWarning},
		},
	})
	addLSPClient(t, a, "tsls", map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {{Severity: protocol.SeverityHint}},
		protocol.URIFromPath("/repo/b.ts"): {{Severity: protocol.SeverityInformation}},
	})

	status, _, body = get("/v1/workspaces/" + workspaceID + "/lsps/diagnostics/by-file")
	require.Equal(t, http.StatusOK, status)
	var byFile map[string]map[string]int
	require.NoError(t, json.Unmarshal(body, &byFile))
	require.Equal(t, map[string]map[string]int{
		"/repo/a.go": {"error": 1, "warning": 1, "information": 0, "hint": 1},
		"/repo/b.ts": {"error": 0, "warning": 0, "information": 1, "hint": 0},
	}, byFile)

	// The per-server route still answers after the batch route was
	// registered.
	status, _, body = get("/v1/workspaces/" + workspaceID + "/lsps/gopls/diagnostics")
	require.Equal(t, http.StatusOK, status)
	var diags map[protocol.DocumentURI][]protocol.Diagnostic
	require.NoError(t, json.Unmarshal(body, &diags))
	require.Len(t, diags[protocol.URIFromPath("/repo/a.go")], 2)
}

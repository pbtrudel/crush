package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// TestWriteWorkspaceErrorMapsAgentStarting is the positive half: a starting
// agent is a state to wait out, answered 503 with the starting code.
func TestWriteWorkspaceErrorMapsAgentStarting(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeWorkspaceError(rec, "ask_failed", fmt.Errorf("ask: %w", workspace.ErrAgentNotInitialized))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"Crush is starting the agent","code":"agent_starting"}`, rec.Body.String())
}

// TestWriteWorkspaceErrorHidesTheInternalText is the negative half: a
// failure with no state to map is a 500 whose body never carries the error,
// which can name a socket or a path.
func TestWriteWorkspaceErrorHidesTheInternalText(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeWorkspaceError(rec, "ask_failed", errors.New("dial unix /tmp/crush.sock: refused"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "crush.sock")
	require.Contains(t, rec.Body.String(), `"code":"ask_failed"`)
}

// TestWriteWorkspaceErrorMapsReconnecting pins that a lost link is a state
// the tab waits out, not an internal failure: a workspace the server no
// longer knows and an unreachable server both answer a 503 with a fixed
// sentence and their own code, so the browser can show reconnecting.
func TestWriteWorkspaceErrorMapsReconnecting(t *testing.T) {
	t.Parallel()
	const text = "Crush is reconnecting to its server. Try again in a moment."
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"workspace gone", fmt.Errorf("reload: %w", workspace.ErrWorkspaceGone), "workspace_gone"},
		{"server unreachable", fmt.Errorf("status code 502: %w", workspace.ErrServerUnreachable), "server_unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			writeWorkspaceError(rec, "summarize", tc.err)
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.JSONEq(t, `{"error":"`+text+`","code":"`+tc.code+`"}`, rec.Body.String())
			require.NotContains(t, rec.Body.String(), workspace.ErrWorkspaceGone.Error())
		})
	}
}

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

func TestWriteWorkspaceErrorMapsAgentStarting(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeWorkspaceError(rec, "ask_failed", fmt.Errorf("ask: %w", workspace.ErrAgentNotInitialized))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"Crush is starting the agent","code":"agent_starting"}`, rec.Body.String())

	rec = httptest.NewRecorder()
	writeWorkspaceError(rec, "ask_failed", errors.New("dial unix /tmp/crush.sock: refused"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "crush.sock")
}

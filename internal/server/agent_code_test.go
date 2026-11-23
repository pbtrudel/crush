package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/require"
)

// TestHandleErrorCarriesAgentCode pins the server API half of the
// agent-not-ready contract: the status stays 400 but the body carries the
// machine code the client SDK maps back to workspace.ErrAgentNotInitialized.
func TestHandleErrorCarriesAgentCode(t *testing.T) {
	t.Parallel()
	h := newE2EHarness(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspace.ID+"/agent/sessions/s1/summarize", nil)
	require.NoError(t, err)
	rsp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)

	var body proto.Error
	require.NoError(t, json.NewDecoder(rsp.Body).Decode(&body))
	require.Equal(t, proto.ErrorCodeAgentNotInitialized, body.Code)
	require.Equal(t, backend.ErrAgentNotInitialized.Error(), body.Message)
}

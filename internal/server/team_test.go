package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/require"
)

// teamStubCoordinator implements only the Team steering methods; the
// embedded nil Coordinator panics on anything else, which flags an
// unexpected call.
type teamStubCoordinator struct {
	agent.Coordinator
	err   error
	calls []string
}

func (c *teamStubCoordinator) TeamSendMessage(_ context.Context, sessionID, member, text string) error {
	c.calls = append(c.calls, "TeamSendMessage:"+sessionID+":"+member+":"+text)
	return c.err
}

func (c *teamStubCoordinator) TeamInterrupt(_ context.Context, sessionID, member string) error {
	c.calls = append(c.calls, "TeamInterrupt:"+sessionID+":"+member)
	return c.err
}

func postTeam(t *testing.T, h *e2eHarness, member, action, body string) (int, proto.Error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspace.ID+"/team/members/"+member+"/"+action,
		bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var e proto.Error
	if resp.StatusCode != http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	}
	return resp.StatusCode, e
}

// TestTeamRoutesCallTheWorkspace pins the request wiring: the session id
// and member come from the body and path, and success answers 200.
func TestTeamRoutesCallTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newE2EHarness(t)
	stub := &teamStubCoordinator{}
	h.app.AgentCoordinator = stub

	status, _ := postTeam(t, h, "alice", "message", `{"session_id":"s1","text":"rebase first"}`)
	require.Equal(t, http.StatusOK, status)
	status, _ = postTeam(t, h, "alice", "interrupt", `{"session_id":"s1"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{
		"TeamSendMessage:s1:alice:rebase first",
		"TeamInterrupt:s1:alice",
	}, stub.calls)
}

// TestTeamRoutesMapErrors pins ruling I1 on the server side: a team
// failure carries a 409 disabled code or the 400 Team code, with the
// message, so the client can restore the original error identity.
func TestTeamRoutesMapErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(*e2eHarness)
		status  int
		code    string
		message string
	}{
		{
			name:    "no coordinator is not initialized",
			prepare: func(*e2eHarness) {},
			status:  http.StatusBadRequest,
			code:    proto.TeamCodeAgentNotInitialized,
			message: backend.ErrAgentNotInitialized.Error(),
		},
		{
			name: "coordinator without team support is disabled",
			prepare: func(h *e2eHarness) {
				h.app.AgentCoordinator = plainCoordinator{}
			},
			status:  http.StatusConflict,
			code:    proto.TeamCodeDisabled,
			message: agent.ErrAgentTeamsDisabled.Error(),
		},
		{
			name: "team service is disabled",
			prepare: func(h *e2eHarness) {
				h.app.AgentCoordinator = &teamStubCoordinator{err: agent.ErrAgentTeamsDisabled}
			},
			status:  http.StatusConflict,
			code:    proto.TeamCodeDisabled,
			message: agent.ErrAgentTeamsDisabled.Error(),
		},
		{
			name: "team refusal keeps its code",
			prepare: func(h *e2eHarness) {
				h.app.AgentCoordinator = &teamStubCoordinator{err: &agentteam.Error{
					Code:    agentteam.CodeMemberNotFound,
					Message: `Team member "alice" not found`,
				}}
			},
			status:  http.StatusBadRequest,
			code:    agentteam.CodeMemberNotFound,
			message: `Team member "alice" not found`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newE2EHarness(t)
			tt.prepare(h)
			for _, action := range []string{"message", "interrupt"} {
				status, body := postTeam(t, h, "alice", action, `{"session_id":"s1","text":"hi"}`)
				require.Equal(t, tt.status, status)
				require.Equal(t, tt.code, body.Code)
				require.Equal(t, tt.message, body.Message)
			}
		})
	}
}

// plainCoordinator satisfies agent.Coordinator without the Team steering
// methods, the way a coordinator predating Agent Teams would.
type plainCoordinator struct {
	agent.Coordinator
}

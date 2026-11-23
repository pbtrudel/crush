package workspace_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/server"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// teamStubCoordinator implements only the Team steering methods; the
// embedded nil Coordinator panics on anything else, which flags an
// unexpected call.
type teamStubCoordinator struct {
	agent.Coordinator
	err error
}

func (c *teamStubCoordinator) TeamSendMessage(context.Context, string, string, string) error {
	return c.err
}

func (c *teamStubCoordinator) TeamInterrupt(context.Context, string, string) error {
	return c.err
}

// teamWorkspaces returns the real server behind an httptest listener and
// both Workspace implementations over it: the local AppWorkspace and the
// ClientWorkspace that goes through the HTTP client.
func teamWorkspaces(t *testing.T, coordinator agent.Coordinator) (*workspace.AppWorkspace, *workspace.ClientWorkspace) {
	t.Helper()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	a.AgentCoordinator = coordinator

	srv := server.NewServer(nil, "tcp", "127.0.0.1:0")
	ws := &backend.Workspace{ID: "ws-1", Path: t.TempDir(), App: a}
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})
	backend.InsertWorkspaceForTest(srv.Backend(), ws)

	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	u, err := url.Parse(hs.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)

	return workspace.NewAppWorkspace(a, nil), workspace.NewClientWorkspace(c, proto.Workspace{ID: ws.ID})
}

// TestTeamErrorIdentityAcrossServerAndClient links the real team handlers
// to the real ClientWorkspace mapping, so a drift between the code the
// server writes and the one the client reads fails here.
func TestTeamErrorIdentityAcrossServerAndClient(t *testing.T) {
	t.Parallel()

	t.Run("wrapped disabled error", func(t *testing.T) {
		t.Parallel()
		appWS, clientWS := teamWorkspaces(t, &teamStubCoordinator{err: fmt.Errorf("session s1: %w", agent.ErrAgentTeamsDisabled)})

		err := clientWS.TeamSendMessage(t.Context(), "s1", "alice", "hi")
		require.ErrorIs(t, err, agent.ErrAgentTeamsDisabled)
		require.EqualError(t, err, agent.ErrAgentTeamsDisabled.Error())

		err = appWS.TeamSendMessage(t.Context(), "s1", "alice", "hi")
		require.ErrorIs(t, err, agent.ErrAgentTeamsDisabled)
	})

	t.Run("team refusal keeps its code", func(t *testing.T) {
		t.Parallel()
		appWS, clientWS := teamWorkspaces(t, &teamStubCoordinator{err: &agentteam.Error{
			Code:    agentteam.CodeMemberNotFound,
			Message: `Team member "alice" not found`,
		}})

		err := clientWS.TeamInterrupt(t.Context(), "s1", "alice")
		require.Equal(t, agentteam.CodeMemberNotFound, agentteam.ErrorCode(err))
		require.EqualError(t, err, `Team member "alice" not found`)

		err = appWS.TeamInterrupt(t.Context(), "s1", "alice")
		require.Equal(t, agentteam.CodeMemberNotFound, agentteam.ErrorCode(err))
	})

	t.Run("missing coordinator is not initialized", func(t *testing.T) {
		t.Parallel()
		appWS, clientWS := teamWorkspaces(t, nil)

		err := clientWS.TeamSendMessage(t.Context(), "s1", "alice", "hi")
		require.ErrorIs(t, err, workspace.ErrAgentNotInitialized)

		err = appWS.TeamSendMessage(t.Context(), "s1", "alice", "hi")
		require.ErrorIs(t, err, workspace.ErrAgentNotInitialized)
	})
}

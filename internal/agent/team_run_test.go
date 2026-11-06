package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
)

func TestTeammateRunRegistersInAgentRunRegistry(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	path := filepath.Join(t.TempDir(), "agents.json")
	coord.agentRuns = agentrun.New(path)

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	sess, err := coord.sessions.CreateTaskSession(t.Context(), "team-member-1", lead.ID, "Teammate reviewer")
	require.NoError(t, err)
	member := agentteam.Member{ID: "member-1", Name: "reviewer", Role: agentteam.RoleTeammate, ParentID: lead.ID}

	coord.startTeammateRun(sess, member)

	runs := agentrun.Load(path)
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.KindTeammate, runs[0].Kind)
	require.Equal(t, "reviewer", runs[0].Name)
	require.Equal(t, lead.ID, runs[0].ParentSessionID)
	require.Equal(t, agentrun.StatusRunning, runs[0].Status)

	coord.finishAgentRun(sess.ID, nil)

	runs = agentrun.Load(path)
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.StatusDone, runs[0].Status)
}

func TestTeammateSessionIDDoesNotCollideWithAnOlderTeam(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	coord.team = service

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.sessions.CreateTaskSession(t.Context(), "team-member-1", lead.ID, "Old teammate")
	require.NoError(t, err)

	runner := &teamRunner{c: coord}
	sess, err := runner.teammateSession(t.Context(), agentteam.Member{
		ID: "member-1", Name: "reviewer", ParentID: lead.ID,
	})
	require.NoError(t, err)
	require.NotEqual(t, "team-member-1", sess.ID, "a new team must not reuse an old teammate session id")
}

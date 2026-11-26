package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// TestTeamAutostartFollowsConfig pins that the service reads the option on
// every pass through the coordinator, so a reload applies without restart.
func TestTeamAutostartFollowsConfig(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	opts := coord.cfg.Config().Options
	require.True(t, coord.teamAutostart())

	off, on := false, true
	opts.AgentTeamAutostart = &off
	require.False(t, coord.teamAutostart())

	opts.AgentTeamAutostart = &on
	opts.AgentTeams = &off
	require.False(t, coord.teamAutostart(), "the pass does nothing with Agent Teams off")
}

func TestTeamRoleExists(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	require.True(t, coord.teamRoleExists("implementer"))
	require.False(t, coord.teamRoleExists("no-such-role"))
}

// TestSyncAgentTeamsPassesAutostart pins the wiring end to end: the service
// syncAgentTeams creates reads the option on every pass.
func TestSyncAgentTeamsPassesAutostart(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	opts := coord.cfg.Config().Options
	off, on := false, true
	opts.AgentTeams = &on
	opts.AgentTeamAutostart = &off
	require.NoError(t, coord.syncAgentTeams())
	team := coord.teamService()
	require.NotNil(t, team)
	t.Cleanup(team.Shutdown)
	runner := newTeamFakeRunner()
	team.SetRunner(runner)

	// With autostart off, even a task that names a role starts nobody.
	first, err := team.CreateTask("lead", "session-lead", agentteam.CreateTaskRequest{
		Subject: "Research", Description: "d", Role: "researcher",
	})
	require.NoError(t, err)
	runner.mu.Lock()
	require.Zero(t, runner.count, "autostart off starts nothing")
	runner.mu.Unlock()

	// Drop that task, so the start below can only come from the task created
	// after the option was turned on.
	_, err = team.UpdateTask("lead", agentteam.UpdateTaskRequest{
		TaskID: first.ID, ExpectedRevision: first.Revision, Action: "delete", SessionID: "session-lead",
	})
	require.NoError(t, err)

	opts.AgentTeamAutostart = &on
	_, err = team.CreateTask("lead", "session-lead", agentteam.CreateTaskRequest{
		Subject: "Build", Description: "d", Role: "researcher",
	})
	require.NoError(t, err)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, 1, runner.count, "the same pass reads the reloaded option")
}

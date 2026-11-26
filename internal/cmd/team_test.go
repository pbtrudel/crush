package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

func TestRenderTeam(t *testing.T) {
	t.Parallel()
	state := agentteam.State{
		TeamID: "team-1",
		Members: []agentteam.Member{
			{ID: "member-1", Name: "reviewer", Role: agentteam.RoleTeammate, Status: agentteam.StatusRunning, Description: "Reviews the diff"},
			{Name: "tester", Role: agentteam.RoleTeammate, Status: agentteam.StatusFailed, Description: "Runs tests", Diagnostics: []string{"provider unavailable"}},
			{Name: "rev", Role: agentteam.RoleTeammate, RoleName: "reviewer", Status: agentteam.StatusInactive, Description: "Second opinion"},
		},
		Tasks: []agentteam.Task{
			{ID: "task-1", Revision: 1, Subject: "Add rate limiting", Description: "d", Status: agentteam.TaskPending, BlockedBy: []string{}, WriteScopes: []string{}},
			{ID: "task-2", Revision: 2, Subject: "Wire middleware", Description: "d", Status: agentteam.TaskInProgress, OwnerID: "member-1", BlockedBy: []string{"task-1"}, WriteScopes: []string{"internal/middleware"}},
			{ID: "task-3", Revision: 1, Subject: "Deleted", Description: "d", Status: agentteam.TaskDeleted, BlockedBy: []string{}, WriteScopes: []string{}},
		},
	}
	output := renderTeam(state)
	require.Contains(t, output, "Members (4)")
	require.Contains(t, output, "reviewer")
	require.Contains(t, output, "rev (reviewer)", "a roster row names the member with its role")
	require.Contains(t, output, "✗ tester")
	require.Contains(t, output, "provider unavailable")
	require.Contains(t, output, "Shared tasks (2)")
	require.Contains(t, output, "Add rate limiting")
	require.Contains(t, output, "owner reviewer")
	require.Contains(t, output, "blocked by task-1")
	require.NotContains(t, output, "Deleted")
}

func TestRenderTeamEmptyBoard(t *testing.T) {
	t.Parallel()
	output := renderTeam(agentteam.State{TeamID: "team-1"})
	require.Contains(t, output, "No shared tasks yet.")
}

func TestTeamUnknownSubcommandIsRejected(t *testing.T) {
	cmd, args, err := rootCmd.Find([]string{"team", "bogus"})
	require.NoError(t, err)
	require.Same(t, teamCmd, cmd)
	require.Equal(t, []string{"bogus"}, args)

	ran, err := executeStraySubcommand(t, "team", "bogus")
	require.ErrorContains(t, err, `unknown command "bogus" for "crush team"`)
	require.False(t, ran, "the roster must not run for an unknown subcommand")
}

func TestTeamFlagsOnlyInvocationRunsTheRoster(t *testing.T) {
	t.Cleanup(func() { teamStatusJSON = false })
	ran, err := executeStraySubcommand(t, "team", "--json")
	require.NoError(t, err)
	require.True(t, ran)
}

func TestRenderTeamPipeline(t *testing.T) {
	t.Parallel()
	off := false
	state := agentteam.State{TeamID: "team-1", Tasks: []agentteam.Task{
		{ID: "task-1", Revision: 3, Subject: "Build it", Description: "d", Status: agentteam.TaskInReview, Role: "implementer", BlockedBy: []string{}, WriteScopes: []string{}},
		{ID: "task-2", Revision: 1, Subject: "Verify: Build it", Description: "d", Status: agentteam.TaskPending, Role: "verifier", Verify: &off, Verifies: "task-1", Attempts: 3, Escalated: true, BlockedBy: []string{}, WriteScopes: []string{}},
	}}
	output := renderTeam(state)
	require.Contains(t, output, "◇ task-1   in_review    Build it")
	require.Contains(t, output, "      role implementer")
	require.Contains(t, output, "      verifies task-1")
	require.Contains(t, output, "      ! escalated after 3 attempts")
}

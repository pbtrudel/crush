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
		},
		Tasks: []agentteam.Task{
			{ID: "task-1", Revision: 1, Subject: "Add rate limiting", Description: "d", Status: agentteam.TaskPending, BlockedBy: []string{}, WriteScopes: []string{}},
			{ID: "task-2", Revision: 2, Subject: "Wire middleware", Description: "d", Status: agentteam.TaskInProgress, OwnerID: "member-1", BlockedBy: []string{"task-1"}, WriteScopes: []string{"internal/middleware"}},
			{ID: "task-3", Revision: 1, Subject: "Deleted", Description: "d", Status: agentteam.TaskDeleted, BlockedBy: []string{}, WriteScopes: []string{}},
		},
	}
	output := renderTeam(state)
	require.Contains(t, output, "Members (3)")
	require.Contains(t, output, "reviewer")
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

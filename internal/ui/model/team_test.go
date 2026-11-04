package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

func TestTeamSignatureChanges(t *testing.T) {
	t.Parallel()
	state := agentteam.State{
		TeamID: "team-1",
		Members: []agentteam.Member{
			{Name: "reviewer", Status: agentteam.StatusRunning},
		},
		Tasks: []agentteam.Task{
			{ID: "task-1", Revision: 1, Status: agentteam.TaskPending},
		},
	}
	first := teamSignature(state)
	require.Equal(t, first, teamSignature(state))

	state.Members[0].Status = agentteam.StatusInactive
	require.NotEqual(t, first, teamSignature(state))

	first = teamSignature(state)
	state.Tasks[0].Revision = 2
	require.NotEqual(t, first, teamSignature(state))
}

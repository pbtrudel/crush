package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
)

func TestSessionMembersFiltersByParentSession(t *testing.T) {
	t.Parallel()
	state := agentteam.State{
		Members: []agentteam.Member{
			{ID: "member-1", Name: "one", ParentID: "session-a"},
			{ID: "member-2", Name: "two", ParentID: "session-b"},
			{ID: "member-3", Name: "three", ParentID: "session-a"},
		},
	}

	members := sessionMembers(state, "session-a")

	require.Len(t, members, 2)
	require.Equal(t, []string{"one", "three"}, []string{members[0].Name, members[1].Name})
}

func TestSessionAgentRunsFiltersByParentSession(t *testing.T) {
	t.Parallel()
	runs := []agentrun.Run{
		{ID: "run-1", ParentSessionID: "session-a"},
		{ID: "run-2", ParentSessionID: "session-b"},
	}

	filtered := sessionAgentRuns(runs, "session-a")

	require.Len(t, filtered, 1)
	require.Equal(t, "run-1", filtered[0].ID)
}

func TestTeamSignatureChanges(t *testing.T) {
	t.Parallel()
	state := agentteam.State{
		TeamID: "team-1",
		Members: []agentteam.Member{
			{Name: "reviewer", Status: agentteam.StatusRunning},
		},
		Tasks: []agentteam.Task{
			{ID: "task-1", Revision: 1, Status: agentteam.TaskPending, Subject: "First"},
		},
	}
	first := teamSignature(state)
	require.Equal(t, first, teamSignature(state))

	state.Members[0].Status = agentteam.StatusInactive
	require.NotEqual(t, first, teamSignature(state))

	first = teamSignature(state)
	state.Tasks[0].Revision = 2
	require.NotEqual(t, first, teamSignature(state))

	first = teamSignature(state)
	state.Tasks[0].Subject = "Renamed"
	require.NotEqual(t, first, teamSignature(state), "editing a task subject must refresh the sidebar")
}

func TestAgentRunsSignatureChanges(t *testing.T) {
	t.Parallel()
	runs := []agentrun.Run{
		{ID: "run-1", Name: "First", Status: agentrun.StatusRunning},
	}
	first := agentRunsSignature(runs)
	require.Equal(t, first, agentRunsSignature(runs))

	runs[0].Status = agentrun.StatusDone
	require.NotEqual(t, first, agentRunsSignature(runs))
}

func TestTeamTaskBucketsKeepsOnlyOwnedLiveTasks(t *testing.T) {
	t.Parallel()
	state := agentteam.State{
		Tasks: []agentteam.Task{
			{ID: "task-1", Status: agentteam.TaskCompleted, Subject: "Done", OwnerID: "member-1"},
			{ID: "task-2", Status: agentteam.TaskDeleted, Subject: "Gone", OwnerID: "member-1"},
			{ID: "task-3", Status: agentteam.TaskPending, Subject: "Blocked", BlockedBy: []string{"task-4"}, OwnerID: "member-1"},
			{ID: "task-4", Status: agentteam.TaskInProgress, Subject: "Running", OwnerID: "member-1"},
			{ID: "task-5", Status: agentteam.TaskPending, Subject: "Ready", OwnerID: "member-1"},
			{ID: "task-6", Status: agentteam.TaskInProgress, Subject: "Other session", OwnerID: "member-2"},
			{ID: "task-7", Status: agentteam.TaskPending, Subject: "Unowned"},
		},
	}
	owners := map[string]bool{"lead": true, "member-1": true}

	active, completed := teamTaskBuckets(state, owners)

	require.Equal(t, 1, completed)
	require.Len(t, active, 3)
	require.Equal(t, []string{"task-4", "task-5", "task-3"}, []string{
		active[0].ID, active[1].ID, active[2].ID,
	}, "in-progress work first, then ready work, then blocked work; other sessions' tasks are hidden")
}

func TestTeamTaskBucketsEmptyBoard(t *testing.T) {
	t.Parallel()

	active, completed := teamTaskBuckets(agentteam.State{}, map[string]bool{})

	require.Empty(t, active)
	require.Zero(t, completed)
}

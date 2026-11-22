package agentteam

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotifySessionIgnoresSessionsWithoutATeammate(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	owned, err := service.NotifySession(context.Background(), "session-a", "job done")
	require.NoError(t, err)
	require.False(t, owned, "the Lead's own session is not a teammate's")
	require.Equal(t, 1, runner.promptCount())
}

func TestNotifySessionQueuesForARunningTeammate(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	owned, err := service.NotifySession(context.Background(), "session-fake", "job done")
	require.NoError(t, err)
	require.True(t, owned)
	require.Equal(t, 1, runner.promptCount(), "a running teammate must not get a second turn")

	pending := service.DrainInbox("reviewer", "session-a")
	require.Len(t, pending, 1)
	require.Equal(t, "job done", pending[0].Text)
}

func TestNotifySessionWakesAnInactiveTeammateOnItsOwnRun(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", "session-a")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	owned, err := service.NotifySession(context.Background(), "session-fake", "job done")
	require.NoError(t, err)
	require.True(t, owned)
	require.Equal(t, 2, runner.promptCount())
	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, "reviewer", runner.members[1])
	require.Contains(t, runner.prompts[1], "job done")
}

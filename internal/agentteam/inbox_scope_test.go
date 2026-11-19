package agentteam

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every Lead session in a workspace answers to the name "lead", so the Lead
// inbox is shared by address. Mail a teammate of one session writes belongs
// to that session's Lead alone: another session's Lead must neither read it
// nor have it marked delivered.
func TestLeadInboxIsScopedToTheLeadSession(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	for _, request := range []SpawnRequest{
		{Name: "worker-a", Description: "d", Prompt: "p", ParentID: "session-a"},
		{Name: "worker-b", Description: "d", Prompt: "p", ParentID: "session-b"},
	} {
		_, err := service.SpawnTeammate(context.Background(), "lead", request)
		require.NoError(t, err)
	}

	runner.finishWithOutput("worker-a", "report for lead-a")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").UnreadByLead == 1
	}, time.Second, 10*time.Millisecond, "the report is unread until its Lead reads it")
	require.Zero(t, service.Tree("session-b").UnreadByLead, "another session's report is not this Lead's")

	require.Empty(t, service.DrainInbox("lead", "session-b"), "another Lead session drained this session's inbox")
	require.Equal(t, 1, service.Tree("session-a").UnreadByLead, "the report still waits for its own Lead")

	pending := service.DrainInbox("lead", "session-a")
	require.Len(t, pending, 1)
	require.Equal(t, "worker-a", pending[0].From)
	require.Zero(t, service.Tree("session-a").UnreadByLead, "the report was read")
}

// A call that carries no session sees no team, so it must not drain an inbox
// either.
func TestDrainInboxWithNoSessionDeliversNothing(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	runner.finishWithOutput("worker", "report")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").UnreadByLead == 1
	}, time.Second, 10*time.Millisecond)

	require.Empty(t, service.DrainInbox("lead", ""))
	require.Equal(t, 1, service.Tree("session-a").UnreadByLead)
}

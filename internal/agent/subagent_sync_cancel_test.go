package agent

import (
	"context"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestCancelOnSynchronousSubagentSessionStopsIt pins that a running
// synchronous subagent is addressable by its child session id: Cancel
// reaches the agent instance the subagent runs on, not just the main one.
func TestCancelOnSynchronousSubagentSessionStopsIt(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	c.dispatched = csync.NewMap[string, *dispatchedAgent]()
	c.mainAgent = newMockAgent(providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("main answer"), nil
	})

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	started := make(chan struct{})
	stopped := make(chan struct{})
	// cancelRun models the agent's own cancel path: the coordinator must
	// call Cancel on this instance for the run to stop.
	cancelRun := make(chan struct{})
	sub := newMockAgent(providerID, 4096, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		select {
		case <-ctx.Done():
		case <-cancelRun:
		}
		close(stopped)
		return nil, ctx.Err()
	})
	sub.onCancel = func(string) { close(cancelRun) }

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.runSubAgent(context.Background(), subAgentParams{
			Agent:          sub,
			SessionID:      parent.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "do something",
			SessionTitle:   "Subagent",
		})
	}()
	<-started

	childID := c.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	c.Cancel(childID)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Cancel with the child session id did not stop a running synchronous subagent")
	}
	<-done
	require.False(t, c.subSessionBusy(childID), "the finished run must leave the bookkeeping")
}

// TestCancelStopsEveryAgentOnASubagentSession covers concurrent runs on
// one child session: each run's own agent instance is cancelled, and the
// bookkeeping is dropped only when the last run ends.
func TestCancelStopsEveryAgentOnASubagentSession(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	c := &coordinator{
		mainAgent:  newMockAgent(providerID, 4096, nil),
		dispatched: csync.NewMap[string, *dispatchedAgent](),
	}
	first := newMockAgent(providerID, 4096, nil)
	second := newMockAgent(providerID, 4096, nil)

	doneFirst := c.trackSubSession("child", first)
	doneSecond := c.trackSubSession("child", second)

	c.Cancel("child")
	require.Equal(t, []string{"child"}, first.cancelled)
	require.Equal(t, []string{"child"}, second.cancelled)

	doneFirst()
	require.True(t, c.subSessionBusy("child"), "a concurrent run keeps the session live")
	doneSecond()
	require.False(t, c.subSessionBusy("child"), "the last run removes the entry")
}

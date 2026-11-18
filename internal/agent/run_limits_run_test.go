package agent

import (
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

// stepCount counts the agent steps of a session: its assistant rows other
// than compaction summaries.
func stepCount(t *testing.T, env fakeEnv, sessionID string) int {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	steps := 0
	for _, msg := range msgs {
		if msg.Role == message.Assistant && !msg.IsSummaryMessage {
			steps++
		}
	}
	return steps
}

// A model that keeps calling tools is stopped once the tree's budget is
// spent, and no run of the tree starts after that.
func TestStepLimitStopsTheTree(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 3})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
	require.Equal(t, 3, stepCount(t, env, sess.ID))

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.ErrorIs(t, err, errTreeLimitReached)
	child, err := env.sessions.CreateTaskSession(t.Context(), "call-1", sess.ID, "Subagent")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: child.ID, Prompt: "go"})
	require.ErrorIs(t, err, errTreeLimitReached)
	require.Equal(t, 3, stepCount(t, env, sess.ID))
	require.Zero(t, stepCount(t, env, child.ID))
}

// A run continued after the context guard is a new run, but the budget is
// the tree's, so the continuation gets only what is left of it.
func TestStepLimitSpansAContinuation(t *testing.T) {
	env := testEnv(t)
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000)
	model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000), summary, callTool("alpha", 1_000)}}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 3})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndContextGuard, message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
	require.Equal(t, 3, stepCount(t, env, sess.ID), "the summary does not draw on the budget")
}

// The command cancels a tree whose deadline passed. That cancel carries no
// cause, so the run reads time_limit from the deadline, not canceled.
func TestTimeLimitOutranksTheCancel(t *testing.T) {
	env := testEnv(t)
	model := &gatedModel{finishStreamModel: finishStreamModel{text: "never"}, release: make(chan struct{})}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	tree := agent.limits.set(sess.ID, RunLimits{Timeout: 100 * time.Millisecond})

	done := make(chan error, 1)
	go func() {
		_, runErr := agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		done <- runErr
	}()
	require.Eventually(t, tree.deadlinePassed, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return agent.IsSessionBusy(sess.ID) }, 5*time.Second, 10*time.Millisecond)
	agent.Cancel(sess.ID)
	select {
	case runErr := <-done:
		require.Error(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop when it was cancelled")
	}
	require.Equal(t, []message.TurnEnd{message.TurnEndTimeLimit}, turnEnds(t, env, sess.ID))

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.ErrorIs(t, err, errTreeLimitReached)
}

// A model that answers on the step that spends the budget had already
// stopped: the run is completed, not cut short.
func TestStepLimitLeavesAFinalAnswer(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 1})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, sess.ID))
}

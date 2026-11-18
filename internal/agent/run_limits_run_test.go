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
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "Time limit reached", msgs[1].FinishPart().Message,
		"the row names the deadline, not a user cancel")

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.ErrorIs(t, err, errTreeLimitReached)
}

// A step that outlasts the tree's deadline ends the run that took it, even
// when no cancel reached the run.
func TestTimeLimitStopsAStepWithoutACancel(t *testing.T) {
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{callTool("sleeper", 1_000), answer("done", fantasy.FinishReasonStop, 1_000)}}
	agent := newCompactionAgent(env, model, nil, false, &slowTool{delay: 150 * time.Millisecond})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{Timeout: 50 * time.Millisecond})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndTimeLimit}, turnEnds(t, env, sess.ID))
	require.Equal(t, 1, stepCount(t, env, sess.ID), "the run stops after the step the deadline passed in")
}

// A queued prompt and a queued report are settled when the tree's limit
// stops the run they were queued behind, instead of being refused one call
// at a time.
func TestStepLimitSettlesQueuedWork(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 1})

	// Queued ahead of the run: a budget of one is spent when the first step
	// starts, so a call made while that step runs is refused at entry and
	// never reaches the queue.
	report := message.SubAgentReport{Label: "akiko-bugs", Output: "all good"}
	agent.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: "follow-up", RunID: "run-2"})
	agent.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: report.PromptText(), Report: &report, RunID: "run-3"})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err, "the run that ran returns its own outcome")
	require.Zero(t, agent.QueuedPrompts(sess.ID), "the queue is not left stranded")
	require.Zero(t, agent.acceptedCount(sess.ID), "no accept reservation is left behind")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, []message.SubAgentReport{report}, reportsIn(t, msgs))
}

// A report that meets the tree's limit is still recorded, and the accept
// reservation it arrived with is released.
func TestTreeLimitRecordsARefusedReport(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 1})
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	report := message.SubAgentReport{Label: "akiko-bugs", Output: "all good"}
	_, err = agent.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    report.PromptText(),
		Report:    &report,
		RunID:     "run-2",
		Accepted:  agent.BeginAccepted(sess.ID),
	})
	require.ErrorIs(t, err, errTreeLimitReached)
	require.Zero(t, agent.acceptedCount(sess.ID), "the refused call releases its accept")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, []message.SubAgentReport{report}, reportsIn(t, msgs), "the refused report is recorded")
}

// A compaction the context guard required cannot continue a turn the tree's
// limit refuses, so the limit is what ended the turn.
func TestStepLimitEndsACompactedTurn(t *testing.T) {
	env := testEnv(t)
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000)
	model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000), summary}}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 1})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
	require.Zero(t, agent.QueuedPrompts(sess.ID), "the refused continuation is not queued")
}

// reportsIn lists every subagent report recorded in the messages.
func reportsIn(t *testing.T, msgs []message.Message) []message.SubAgentReport {
	t.Helper()
	var reports []message.SubAgentReport
	for _, msg := range msgs {
		reports = append(reports, msg.SubAgentReports()...)
	}
	return reports
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

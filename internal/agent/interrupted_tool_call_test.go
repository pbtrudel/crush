package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// killedTurn seeds the shape a process killed mid-tool-call leaves behind: an
// assistant message holding a tool call, no tool result anywhere, and no
// finish part. finished says whether the call's arguments had finished
// arriving before the process died.
func killedTurn(t *testing.T, env fakeEnv, finished bool) (session.Session, message.Message) {
	t.Helper()
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "killed")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "run the thing"}},
	})
	require.NoError(t, err)

	input := `{"command":"sleep 30"}`
	if !finished {
		input = ""
	}
	assistant, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{
				ID:       "call_killed",
				Name:     "bash",
				Input:    input,
				Finished: finished,
			},
		},
	})
	require.NoError(t, err)
	return sess, assistant
}

func TestRepairInterruptedToolCalls_WritesTheRepair(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, false)

	listSettled(t, env, sess.ID)

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	var result *message.ToolResult
	for _, msg := range stored {
		for _, tr := range msg.ToolResults() {
			if tr.ToolCallID == "call_killed" {
				tr := tr
				result = &tr
			}
		}
	}
	require.NotNil(t, result, "the orphaned call must be given a stored result")
	require.True(t, result.IsError)
	require.Equal(t, interruptedToolCallResult, result.Content)

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	calls := reloaded.ToolCalls()
	require.Len(t, calls, 1)
	require.True(t, calls[0].Finished, "a call that never got its arguments is settled")
	require.Empty(t, calls[0].Input, "arguments that never arrived are not invented")
	require.True(t, reloaded.IsFinished(), "the turn is marked finished")
}

// The finish part records when the turn actually stopped. Stamping it with
// now would tell the response-time statistics that a turn killed months ago
// took months to answer.
func TestRepairInterruptedToolCalls_BackdatesTheFinish(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, true)

	before, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)

	listSettled(t, env, sess.ID)

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	finish := reloaded.FinishPart()
	require.NotNil(t, finish)
	require.Equal(t, before.UpdatedAt, finish.Time, "the finish is stamped with when the turn stopped")
	require.Empty(t, finish.Build, "the repairing build did not write the turn")
}

// A turn can end cleanly and still lose a tool result, so a present finish
// part must not be taken as proof that nothing is dangling.
func TestRepairInterruptedToolCalls_RepairsFinishedTurn(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "finished-but-orphaned")
	require.NoError(t, err)

	assistant, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_a", Name: "bash", Input: "{}", Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 1000},
		},
	})
	require.NoError(t, err)

	listSettled(t, env, sess.ID)

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var found bool
	for _, msg := range stored {
		for _, tr := range msg.ToolResults() {
			found = found || tr.ToolCallID == "call_a"
		}
	}
	require.True(t, found, "an orphan on a finished turn is still repaired")

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1000), reloaded.FinishPart().Time, "an existing finish is left alone")
}

func TestRepairInterruptedToolCalls_LeavesSettledCallsAlone(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "healthy")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_ok", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 1000},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_ok", Name: "bash", Content: "ok"}},
	})
	require.NoError(t, err)

	before, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	listSettled(t, env, sess.ID)
	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, len(before), "a healthy session gains no rows")

	after, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, after, len(before))
}

// Repairing twice must not stack up results, since every turn re-reads the
// session.
func TestRepairInterruptedToolCalls_IsIdempotent(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, _ := killedTurn(t, env, true)

	listSettled(t, env, sess.ID)
	first, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	listSettled(t, env, sess.ID)
	second, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	require.Len(t, second, len(first), "a second pass adds nothing")
}

// Parallel tool calls die together, and each needs its own result or the
// provider rejects the whole message.
func TestRepairInterruptedToolCalls_HandlesParallelCalls(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "parallel")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
			message.ToolCall{ID: "call_2", Name: "view", Input: "", Finished: false},
			message.ToolCall{ID: "call_3", Name: "grep", Input: `{"pattern":"x"}`, Finished: true},
		},
	})
	require.NoError(t, err)
	// Only the middle call got an answer before the process died.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_2", Name: "view", Content: "file"}},
	})
	require.NoError(t, err)

	listSettled(t, env, sess.ID)

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	results := map[string]message.ToolResult{}
	for _, msg := range stored {
		for _, tr := range msg.ToolResults() {
			results[tr.ToolCallID] = tr
		}
	}
	require.Len(t, results, 3, "every call ends up with exactly one result")
	require.Equal(t, "file", results["call_2"].Content, "the real result is left alone")
	require.True(t, results["call_1"].IsError)
	require.True(t, results["call_3"].IsError)
}

// A reader of the transcript settles orphans too, not just the next run.
// Without this a session opened after a crash shows the call as still in
// flight, and the first message of the next run rewrites the transcript
// under the user.
func TestListSettledMessages_SettlesBeforeAnyRun(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, false)

	listSettled(t, env, sess.ID)

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var result *message.ToolResult
	for _, msg := range stored {
		for _, tr := range msg.ToolResults() {
			if tr.ToolCallID == "call_killed" {
				tr := tr
				result = &tr
			}
		}
	}
	require.NotNil(t, result, "opening the session must settle the orphan")

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.Empty(t, reloaded.ToolCalls()[0].Input)
	require.True(t, reloaded.IsFinished())
}

// Arguments can stop arriving halfway through. What the model managed to
// write stays in the transcript; only what goes to the provider is repaired.
func TestListSettledMessages_KeepsHalfWrittenArguments(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	const partial = `{"command":"sleep 3`

	sess, err := env.sessions.Create(ctx, "half-written")
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "call_partial", Name: "bash", Input: partial}},
	})
	require.NoError(t, err)

	listSettled(t, env, sess.ID)

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	call := reloaded.ToolCalls()[0]
	require.True(t, call.Finished, "the call is settled")
	require.Equal(t, partial, call.Input, "the transcript keeps what the model wrote")

	for _, part := range reloaded.ToAIMessage()[0].Content {
		if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
			require.Equal(t, "{}", tc.Input, "the send path is what makes it valid JSON")
		}
	}
}

func TestListSettledMessages_HealthySessionIsUntouched(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "healthy")
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_ok", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 1000},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_ok", Name: "bash", Content: "ok"}},
	})
	require.NoError(t, err)

	before, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	listSettled(t, env, sess.ID)
	after, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, after, len(before), "settling a healthy session writes nothing")
}

// listSettled reads a session through the coordinator, which settles orphans
// first.
func listSettled(t *testing.T, env fakeEnv, sessionID string) []message.Message {
	t.Helper()
	msgs, err := settlingCoordinator(env).ListSettledMessages(t.Context(), sessionID)
	require.NoError(t, err)
	return msgs
}

// The returned transcript is the stored one, so callers need no second read.
func TestListSettledMessages_ReturnsWhatWasStored(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, _ := killedTurn(t, env, true)

	got := listSettled(t, env, sess.ID)
	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, got, len(stored))
	for i := range stored {
		require.Equal(t, stored[i].ID, got[i].ID)
		require.Equal(t, stored[i].Parts, got[i].Parts)
	}
}

func settlingCoordinator(env fakeEnv) *coordinator {
	main := NewSessionAgent(SessionAgentOptions{Models: noModels, Sessions: env.sessions, Messages: env.messages})
	return &coordinator{mainAgent: main, agents: map[string]SessionAgent{config.AgentCoder: main}, messages: env.messages}
}

// A live run's unanswered call is not an orphan yet, so it is left alone,
// whether the main agent is running it or a subagent on its own instance.
func TestListSettledMessages_SkipsBusySessions(t *testing.T) {
	for name, markBusy := range map[string]func(t *testing.T, c *coordinator, sessionID string){
		"main agent": func(_ *testing.T, c *coordinator, sessionID string) {
			c.mainAgent.(*sessionAgent).activeRequests.Set(sessionID, &activeCancel{cancel: func() {}})
		},
		"subagent": func(t *testing.T, c *coordinator, sessionID string) {
			t.Cleanup(c.trackSubSession(sessionID, nil))
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			sess, _ := killedTurn(t, env, true)
			c := settlingCoordinator(env)
			markBusy(t, c, sess.ID)

			msgs, err := c.ListSettledMessages(t.Context(), sess.ID)
			require.NoError(t, err)
			for _, m := range msgs {
				require.Empty(t, m.ToolResults(), "a live run's unanswered call must be left alone")
			}
		})
	}
}

// A dispatched subagent runs on an agent instance of its own, so a reader
// holding the main agent sees its session as idle. Reading the child
// transcript through the coordinator must still leave the live run's calls
// alone: only the coordinator knows a subagent owns them.
func TestListSettledMessages_LeavesALiveSubagentAlone(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	child, assistant := killedTurn(t, env, true)

	c := settlingCoordinator(env)

	// Marked busy the same way a dispatched run does, on the coordinator
	// rather than on the agent instance the reader holds.
	untrack := c.trackSubSession(child.ID, nil)

	msgs, err := c.ListSettledMessages(ctx, child.ID)
	require.NoError(t, err)
	for _, m := range msgs {
		require.Empty(t, m.ToolResults(), "a live subagent's unanswered call must be left alone")
	}
	stored, err := env.messages.List(ctx, child.ID)
	require.NoError(t, err)
	for _, m := range stored {
		require.Empty(t, m.ToolResults(), "reading a live subagent must write no settle row")
	}
	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.False(t, reloaded.IsFinished(), "a live subagent's turn is not finished by a read")

	// Once the subagent ends, the same read repairs what it left behind.
	untrack()
	msgs, err = c.ListSettledMessages(ctx, child.ID)
	require.NoError(t, err)
	var settled []message.ToolResult
	for _, m := range msgs {
		settled = append(settled, m.ToolResults()...)
	}
	require.Len(t, settled, 1, "the orphaned call is settled once the subagent is gone")
	require.Equal(t, interruptedToolCallResult, settled[0].Content)
}

// Switching coder to plan must not make a run still going on coder look idle,
// or a reader would settle its in-flight calls.
func TestCoordinatorIsSessionBusy_SeesEveryMainAgent(t *testing.T) {
	env := testEnv(t)
	coder := NewSessionAgent(SessionAgentOptions{Models: noModels, Sessions: env.sessions, Messages: env.messages}).(*sessionAgent)
	plan := NewSessionAgent(SessionAgentOptions{Models: noModels, Sessions: env.sessions, Messages: env.messages})
	c := &coordinator{mainAgent: plan, agents: map[string]SessionAgent{"coder": coder, "plan": plan}}

	coder.activeRequests.Set("sess", &activeCancel{cancel: func() {}})
	require.True(t, c.IsSessionBusy("sess"))
	require.False(t, c.IsSessionBusy("other"))
}

// Several clients can reload one session at once, say after a reconnect.
// Each must not settle the same orphan, or the call ends up with two results.
func TestListSettledMessages_ConcurrentReadersSettleOnce(t *testing.T) {
	env := testEnv(t)
	sess, _ := killedTurn(t, env, true)
	c := settlingCoordinator(env)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := c.ListSettledMessages(t.Context(), sess.ID)
			require.NoError(t, err)
		})
	}
	wg.Wait()

	stored, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var results int
	for _, m := range stored {
		results += len(m.ToolResults())
	}
	require.Equal(t, 1, results, "the orphan must be settled exactly once")
}

// TestSettleSession_RepairsAnIdleTranscript pins what paging a session needs:
// the web gateway reads pages of the store, so the settle has to happen
// before the page is read, not on the way through it.
func TestSettleSession_RepairsAnIdleTranscript(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, true)
	c := settlingCoordinator(env)

	require.NoError(t, c.SettleSession(ctx, sess.ID))

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var results []message.ToolResult
	for _, m := range stored {
		results = append(results, m.ToolResults()...)
	}
	require.Len(t, results, 1, "an idle session is settled before it is paged")
	require.Equal(t, interruptedToolCallResult, results[0].Content)
	require.True(t, results[0].IsError)

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.True(t, reloaded.ToolCalls()[0].Finished)
}

// A live run's unanswered call is not an orphan, so settling leaves it and
// the pager's page shows it exactly as stored.
func TestSettleSession_LeavesABusySessionAlone(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, true)
	c := settlingCoordinator(env)
	c.mainAgent.(*sessionAgent).activeRequests.Set(sess.ID, &activeCancel{cancel: func() {}})

	require.NoError(t, c.SettleSession(ctx, sess.ID))

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	for _, m := range stored {
		require.Empty(t, m.ToolResults(), "a busy session is paged without a repair")
	}
	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.False(t, reloaded.IsFinished(), "a live run's turn is not finished by settling")
}

// A call that already has its result must not gain a second one: only the
// rows settling needs are read, and the results are read with them.
func TestSettleSession_DoesNotAnswerAnAnsweredCall(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, _ := env.sessions.Create(ctx, "answered")
	_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_a", Name: "bash", Input: "{}", Finished: true},
			message.ToolCall{ID: "call_b", Name: "view", Input: "{}", Finished: true},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_a", Name: "bash", Content: "done"}},
	})
	require.NoError(t, err)

	c := settlingCoordinator(env)
	require.NoError(t, c.SettleSession(ctx, sess.ID))

	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var results []message.ToolResult
	for _, m := range stored {
		results = append(results, m.ToolResults()...)
	}
	require.Len(t, results, 2, "each call ends with exactly one result")
	byCall := map[string]string{}
	for _, tr := range results {
		byCall[tr.ToolCallID] = tr.Content
	}
	require.Equal(t, "done", byCall["call_a"], "the stored result is left alone")
	require.Equal(t, interruptedToolCallResult, byCall["call_b"])
}

// countingMessages counts the settle reads, so a test can pin that a healthy
// session never loads its tool rows.
type countingMessages struct {
	message.Service
	toolReads int
	toolRows  int
}

func (c *countingMessages) ListToolMessagesFrom(ctx context.Context, sessionID string, from int64) ([]message.Message, error) {
	c.toolReads++
	rows, err := c.Service.ListToolMessagesFrom(ctx, sessionID, from)
	c.toolRows += len(rows)
	return rows, err
}

// flakyCreateMessages fails the first tool-result write, the way a store can
// fail part way through a repair.
type flakyCreateMessages struct {
	message.Service
	failed bool
}

func (f *flakyCreateMessages) Create(ctx context.Context, sessionID string, params message.CreateMessageParams) (message.Message, error) {
	if params.Role == message.Tool && !f.failed {
		f.failed = true
		return message.Message{}, errors.New("store is busy")
	}
	return f.Service.Create(ctx, sessionID, params)
}

// settlingCoordinatorOver builds a coordinator over another message service,
// so a test can count or fail the settle's writes.
func settlingCoordinatorOver(env fakeEnv, messages message.Service) *coordinator {
	main := NewSessionAgent(SessionAgentOptions{Models: noModels, Sessions: env.sessions, Messages: messages})
	return &coordinator{mainAgent: main, agents: map[string]SessionAgent{config.AgentCoder: main}, messages: messages}
}

// TestSettleSession_ReadsNoToolRowsForAHealthySession pins the cost of a page
// read: a session with nothing unfinished never loads its tool rows, which
// are the bulk of a long transcript.
func TestSettleSession_ReadsNoToolRowsForAHealthySession(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "healthy")
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_ok", Name: "bash", Input: "{}", Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 1000},
		},
	})
	require.NoError(t, err)
	// Update is what sets finished_at, so this turn is done.
	require.NoError(t, env.messages.Update(ctx, assistant))
	for range 50 {
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  message.Tool,
			Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_ok", Name: "bash", Content: "ok"}},
		})
		require.NoError(t, err)
	}

	// A turn whose only call the provider ran itself is unfinished too, but
	// it can never need a result, so settling it must still read no tool row.
	providerOnly, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_provider", Name: "web_search", ProviderExecuted: true, Finished: true},
		},
	})
	require.NoError(t, err)

	counting := &countingMessages{Service: env.messages}
	c := settlingCoordinatorOver(env, counting)
	require.NoError(t, c.SettleSession(ctx, sess.ID))
	require.Zero(t, counting.toolReads, "a healthy session must read no tool rows")
	require.Zero(t, counting.toolRows)

	reloaded, err := env.messages.Get(ctx, providerOnly.ID)
	require.NoError(t, err)
	require.True(t, reloaded.IsFinished(), "a provider-only turn is finished without a tool lookup")
}

// TestSettleSession_RetriesARepairThatStoppedHalfway pins that a repair which
// failed on its result write is picked up by the next read instead of leaving
// the call open forever.
func TestSettleSession_RetriesARepairThatStoppedHalfway(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, assistant := killedTurn(t, env, true)
	flaky := &flakyCreateMessages{Service: env.messages}
	c := settlingCoordinatorOver(env, flaky)

	require.NoError(t, c.SettleSession(ctx, sess.ID))
	stored, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	for _, m := range stored {
		require.Empty(t, m.ToolResults(), "the first pass wrote no result")
	}
	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	require.False(t, reloaded.IsFinished(), "a repair that stopped halfway leaves the turn open")

	require.NoError(t, c.SettleSession(ctx, sess.ID))
	stored, err = env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var results []message.ToolResult
	for _, m := range stored {
		results = append(results, m.ToolResults()...)
	}
	require.Len(t, results, 1, "the next read settles the call")
	require.Equal(t, interruptedToolCallResult, results[0].Content)
}

// resultsIn counts the tool results stored for one call.
func resultsIn(t *testing.T, messages message.Service, sessionID, callID string) int {
	t.Helper()
	stored, err := messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var n int
	for _, m := range stored {
		for _, tr := range m.ToolResults() {
			if tr.ToolCallID == callID {
				n++
			}
		}
	}
	return n
}

// stalledSettleMessages widens the window the settle lock has to close: every
// pass pauses inside its first read, so overlapping callers would all see the
// same open call if they were not serialised.
type stalledSettleMessages struct {
	message.Service
}

func (s *stalledSettleMessages) ListUnfinishedAssistants(ctx context.Context, sessionID string) ([]message.Message, error) {
	time.Sleep(5 * time.Millisecond)
	return s.Service.ListUnfinishedAssistants(ctx, sessionID)
}

// TestSettleSession_ConcurrentSettlesWriteOneResult pins the one lock both
// settle paths share. A tab that pages through the coordinator and a tab that
// pages before the agent exists reach the settle through different callers, so
// without a common lock both would write a result for the same call.
func TestSettleSession_ConcurrentSettlesWriteOneResult(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, _ := killedTurn(t, env, true)
	messages := &stalledSettleMessages{Service: env.messages}
	c := settlingCoordinatorOver(env, messages)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			require.NoError(t, c.SettleSession(ctx, sess.ID))
		})
		wg.Go(func() {
			require.NoError(t, SettleSession(ctx, messages, func(string) bool { return false }, sess.ID))
		})
	}
	wg.Wait()

	require.Equal(t, 1, resultsIn(t, env.messages, sess.ID, "call_killed"),
		"the orphan is settled exactly once")
}

// TestSettleSession_FinishesATurnItsResultsAlreadyAnswer pins the turn a
// process killed between its last result and the end of the step: its calls
// are answered, so nothing is written, but the turn needs its finish part or
// every page read keeps selecting it and reading its tool rows.
func TestSettleSession_FinishesATurnItsResultsAlreadyAnswer(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "stalled")
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_a", Name: "bash", Input: "{}", Finished: true},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_a", Name: "bash", Content: "ok"}},
	})
	require.NoError(t, err)

	before, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)

	counting := &countingMessages{Service: env.messages}
	c := settlingCoordinatorOver(env, counting)
	require.NoError(t, c.SettleSession(ctx, sess.ID))

	reloaded, err := env.messages.Get(ctx, assistant.ID)
	require.NoError(t, err)
	finish := reloaded.FinishPart()
	require.NotNil(t, finish, "the stalled turn is finished")
	require.Equal(t, message.FinishReasonError, finish.Reason)
	require.Equal(t, interruptedTurnMessage, finish.Message)
	require.Equal(t, before.UpdatedAt, finish.Time, "the finish is backdated to when the turn stopped")
	require.Equal(t, 1, resultsIn(t, env.messages, sess.ID, "call_a"), "no new result is written")

	// The next pass has nothing unfinished, so it reads no tool row at all.
	reads := counting.toolReads
	require.NoError(t, c.SettleSession(ctx, sess.ID))
	require.Equal(t, reads, counting.toolReads)
}

// TestSettleSession_IgnoresToolRowsOlderThanTheTurn pins the bound on the
// tool read: a result created before the oldest unfinished turn cannot answer
// its calls, so it is neither read nor taken as an answer.
func TestSettleSession_IgnoresToolRowsOlderThanTheTurn(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	sess, err := env.sessions.Create(ctx, "backdated")
	require.NoError(t, err)
	old, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: "call_open", Name: "bash", Content: "stale"}},
	})
	require.NoError(t, err)
	_, err = env.conn.ExecContext(ctx,
		`UPDATE messages SET created_at = created_at - 3600 WHERE id = ?`, old.ID)
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_open", Name: "bash", Input: "{}", Finished: true},
		},
	})
	require.NoError(t, err)

	counting := &countingMessages{Service: env.messages}
	c := settlingCoordinatorOver(env, counting)
	require.NoError(t, c.SettleSession(ctx, sess.ID))

	require.Zero(t, counting.toolRows, "a row older than the turn is not read")
	require.Equal(t, 2, resultsIn(t, env.messages, sess.ID, "call_open"),
		"the stale result did not answer the call, so it is settled anew")
}

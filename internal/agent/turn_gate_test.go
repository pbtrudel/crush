package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// fakeGateCheck is a gate check a test scripts. It counts its calls,
// remembers their input, and can hold a call until the test releases it.
type fakeGateCheck struct {
	name   string
	reason string
	stop   bool
	// want, when set, asks for a continuation only for a reply that
	// contains it, so one check can serve both turns of a test.
	want string
	// block, when non-nil, holds the check until the test closes it.
	block chan struct{}
	// entered, when non-nil, receives one value when a check starts. It
	// is buffered so the check never waits for the test.
	entered chan struct{}

	mu     sync.Mutex
	calls  int
	inputs []gateInput
}

func (c *fakeGateCheck) Name() string { return c.name }

func (c *fakeGateCheck) Check(ctx context.Context, in gateInput) gateVerdict {
	c.mu.Lock()
	c.calls++
	c.inputs = append(c.inputs, in)
	c.mu.Unlock()
	if c.entered != nil {
		// Never wait for the test: a later check would hang the run.
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return gateVerdict{}
		}
	}
	if c.want != "" && !strings.Contains(in.Reply, c.want) {
		return gateVerdict{}
	}
	return gateVerdict{Reason: c.reason, Stop: c.stop}
}

func (c *fakeGateCheck) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *fakeGateCheck) recorded() []gateInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.inputs)
}

// promptRecordingModel records the prompt of every call that carries
// tools, which is every run of a turn, so a test can read what the model
// was sent.
type promptRecordingModel struct {
	*scriptedModel
	mu    sync.Mutex
	calls []recordedCall
}

// recordedCall is one request the model saw.
type recordedCall struct {
	prompt []fantasy.Message
	tools  []string
}

func (m *promptRecordingModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) > 0 {
		recorded := recordedCall{prompt: slices.Clone(call.Prompt)}
		for _, tool := range call.Tools {
			recorded.tools = append(recorded.tools, tool.GetName())
		}
		m.mu.Lock()
		m.calls = append(m.calls, recorded)
		m.mu.Unlock()
	}
	return m.scriptedModel.Stream(ctx, call)
}

// toolNames returns the tools offered to the call at index i.
func (m *promptRecordingModel) toolNames(t *testing.T, i int) []string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Less(t, i, len(m.calls), "the model saw fewer calls than the test expected")
	return m.calls[i].tools
}

// userTexts returns the text of every user message of the prompt at index
// i of the recorded calls.
func (m *promptRecordingModel) userTexts(t *testing.T, i int) []string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Less(t, i, len(m.calls), "the model saw fewer calls than the test expected")
	var texts []string
	for _, msg := range m.calls[i].prompt {
		if msg.Role != fantasy.MessageRoleUser {
			continue
		}
		var b strings.Builder
		for _, part := range msg.Content {
			if text, ok := part.(fantasy.TextPart); ok {
				b.WriteString(text.Text)
			}
		}
		texts = append(texts, b.String())
	}
	return texts
}

// newGateAgent builds an agent over a scripted model whose gate evaluates
// checks.
func newGateAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel, checks ...gateCheck) *sessionAgent {
	t.Helper()
	sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	sa.gateChecks = checks
	return sa
}

// capperAgent is newGateAgent with a turn_end_max_continuations setting.
func capperAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel, maxContinuations int, checks ...gateCheck) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Options.TurnEndMaxContinuations = &maxContinuations
	sa := newCompactionAgent(env, model, cfg, false, &recordingTool{name: "alpha"})
	sa.gateChecks = checks
	return sa
}

// gateRows returns the hidden rows of the session whose text opens with
// the gate tag.
func gateRows(t *testing.T, env fakeEnv, sessionID string) []message.Message {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var rows []message.Message
	for _, msg := range msgs {
		if msg.Role == message.User && strings.HasPrefix(msg.Content().Text, gateOpenTag) {
			rows = append(rows, msg)
		}
	}
	return rows
}

// newGateRunCompleteAgent builds an agent wired to a RunComplete broker, so
// a test can count the terminal events of one RunID.
func newGateRunCompleteAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel, checks ...gateCheck) (*sessionAgent, *pubsub.Broker[notify.RunComplete]) {
	t.Helper()
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	large := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200_000, DefaultMaxTokens: 10_000}}
	sa := NewSessionAgent(SessionAgentOptions{
		Models:       func() ModelSet { return ModelSet{Large: large, Small: large} },
		SystemPrompt: "SYSTEM PROMPT",
		Sessions:     env.sessions,
		Messages:     env.messages,
		RunComplete:  broker,
		Tools:        []fantasy.AgentTool{&recordingTool{name: "alpha"}},
		titles:       env.titles,
	}).(*sessionAgent)
	sa.gateChecks = checks
	return sa, broker
}

// A check that asks for a continuation gets one more run, whose first row
// is the harness row: hidden, tagged, and labelled with the check.
func TestTurnGate_ContinuesOnce(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "check the work"}
	sa := newGateAgent(t, env, model, check)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 2, model.callCount(), "the turn continues in one more run")
	rows := gateRows(t, env, sess.ID)
	require.Len(t, rows, 1, "the continuation opens with one harness row")
	require.True(t, rows[0].Content().Hidden, "the harness row stays out of the chat view")
	require.Contains(t, rows[0].Content().Text, "[check: fake]\ncheck the work")
	require.Equal(t, "fixed", finalAssistantText(t, env, sess.ID), "the continuation's reply reaches the user")
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted, message.TurnEndCompleted}, turnEnds(t, env, sess.ID))
}

// The per-prompt cap bounds the continuations: one by default.
func TestTurnGate_CapHolds(t *testing.T) {
	t.Parallel()

	t.Run("default cap of one", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{
			answer("done", fantasy.FinishReasonStop, 1_000),
			answer("fixed", fantasy.FinishReasonStop, 1_000),
		}}
		check := &fakeGateCheck{name: "fake", reason: "keep going"}
		sa := newGateAgent(t, env, model, check)
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Equal(t, 2, model.callCount(), "one continuation, then the cap holds")
		require.Len(t, gateRows(t, env, sess.ID), 1)
		require.Equal(t, 2, check.callCount(), "the check is asked about both endings")
	})

	t.Run("cap of two", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{
			answer("one", fantasy.FinishReasonStop, 1_000),
			answer("two", fantasy.FinishReasonStop, 1_000),
			answer("three", fantasy.FinishReasonStop, 1_000),
		}}
		check := &fakeGateCheck{name: "fake", reason: "keep going"}
		sa := capperAgent(t, env, model, 2, check)
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Equal(t, 3, model.callCount())
		require.Len(t, gateRows(t, env, sess.ID), 2)
		require.Equal(t, []int{0, 1, 2}, recordedContinuations(check))
	})
}

// recordedContinuations lists the continuation count each check call saw.
func recordedContinuations(check *fakeGateCheck) []int {
	var counts []int
	for _, in := range check.recorded() {
		counts = append(counts, in.Continuations)
	}
	return counts
}

// A cap of zero turns continuations off, and the check still runs once per
// candidate ending.
func TestTurnGate_ZeroDisables(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	sa := capperAgent(t, env, model, 0, check)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 1, model.callCount(), "no continuation runs")
	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, 1, check.callCount(), "the check is observed even when it cannot continue the turn")
}

// A prompt the user queued before the gate takes the next run, so no
// built-in check is asked about the ending.
func TestTurnGate_QueuedUserPromptWinsBeforeTheGate(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("second", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "keep going", want: "done"}
	sa := newGateAgent(t, env, model, check)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)
	// A RunID keeps the queued prompt out of the running turn's steps.
	sa.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: "next", RunID: "run-next"})

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Empty(t, gateRows(t, env, sess.ID), "the user's prompt takes the next run")
	require.Equal(t, "second", finalAssistantText(t, env, sess.ID))
	require.Equal(t, 1, check.callCount(), "only the user's own turn asked the check")
	require.Equal(t, "second", check.recorded()[0].Reply)
}

// A prompt that arrives while a check runs drops the continuation: the
// reply is sent once and the user's prompt runs next.
func TestTurnGate_QueuedUserPromptDropsTheContinuation(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().MCP["signal"] = config.MCPConfig{ChannelReply: &config.MCPChannelReply{
		User: &config.MCPChannelReplyRoute{Tool: "send_message_to_user", TargetParam: "user_id"},
	}}
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("second", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{
		name:    "fake",
		reason:  "keep going",
		want:    "done",
		block:   make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
	sa := newCompactionAgent(env, model, cfg, false, &recordingTool{name: "alpha"})
	sa.gateChecks = []gateCheck{check}
	var replies []string
	sa.replyToolRun = func(_ context.Context, _ *config.ConfigStore, _, tool, input string) (mcp.ToolResult, error) {
		replies = append(replies, tool+" "+input)
		return mcp.ToolResult{Type: "text", Content: "sent"}, nil
	}
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			SessionID:   sess.ID,
			Prompt:      "go",
			Channel:     "signal",
			channelMeta: map[string]string{"sender": "+15551234567"},
		})
		done <- runErr
	}()
	select {
	case <-check.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate never reached the check")
	}
	sa.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: "next", RunID: "run-next"})
	close(check.block)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
	}

	require.Len(t, replies, 1, "the reply is sent exactly once")
	require.Contains(t, replies[0], "done")
	require.NotContains(t, replies[0], "second", "the user's prompt is not folded into the reply")
	require.Empty(t, gateRows(t, env, sess.ID), "the continuation is dropped")
	require.Equal(t, "second", finalAssistantText(t, env, sess.ID))
}

// A queued subagent report does not stop a continuation; it is folded into
// the continuation's first step.
func TestTurnGate_QueuedReportKeepsTheContinuation(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	inner := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	model := &promptRecordingModel{scriptedModel: inner}
	check := &fakeGateCheck{
		name:    "fake",
		reason:  "keep going",
		want:    "done",
		block:   make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
	sa := newGateAgent(t, env, model, check)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		done <- runErr
	}()
	select {
	case <-check.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate never reached the check")
	}
	// A report carries no RunID, as the dispatch report path sends it.
	report := message.SubAgentReport{Label: "worker", Output: "all good"}
	sa.enqueueCall(SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    report.PromptText(),
		Report:    &report,
	})
	close(check.block)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
	}

	require.Equal(t, 2, inner.callCount())
	require.Len(t, gateRows(t, env, sess.ID), 1, "the report does not drop the continuation")
	require.Contains(t, strings.Join(model.userTexts(t, 1), "\n"), "all good",
		"the report is folded into the continuation's first step")
	require.Zero(t, sa.QueuedPrompts(sess.ID))
}

// A cancel during a check ends the turn with no continuation, one reply
// and one terminal event.
func TestTurnGate_CancelDuringTheCheck(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().MCP["signal"] = config.MCPConfig{ChannelReply: &config.MCPChannelReply{
		User: &config.MCPChannelReplyRoute{Tool: "send_message_to_user", TargetParam: "user_id"},
	}}
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	check := &fakeGateCheck{
		name:    "fake",
		reason:  "keep going",
		block:   make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
	sa, broker := newGateRunCompleteAgent(t, env, model, check)
	sa.cfg = cfg
	var replies []string
	sa.replyToolRun = func(_ context.Context, _ *config.ConfigStore, _, tool, input string) (mcp.ToolResult, error) {
		replies = append(replies, tool+" "+input)
		return mcp.ToolResult{Type: "text", Content: "sent"}, nil
	}
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := broker.Subscribe(ctx)
	done := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			SessionID:   sess.ID,
			RunID:       "run-1",
			Prompt:      "go",
			Channel:     "signal",
			channelMeta: map[string]string{"sender": "+15551234567"},
		})
		done <- runErr
	}()
	select {
	case <-check.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate never reached the check")
	}
	sa.Cancel(sess.ID)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish after the cancel")
	}

	require.Len(t, replies, 1, "the reply is sent exactly once")
	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, 1, model.callCount())
	require.Equal(t, "run-1", (<-events).Payload.RunID)
	select {
	case extra := <-events:
		t.Fatalf("a second RunComplete arrived for RunID %q", extra.Payload.RunID)
	case <-time.After(200 * time.Millisecond):
	}
}

// A tree whose step budget is spent does not continue, even though the
// model finished its answer.
func TestTurnGate_StepLimitDropsTheContinuation(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	sa := newGateAgent(t, env, model, check)
	sa.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)
	sa.limits.set(sess.ID, RunLimits{MaxSteps: 1})

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 1, model.callCount())
	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, 1, check.callCount(), "the check still sees the completed ending")
	require.Equal(t, message.TurnEndCompleted, check.recorded()[0].TurnEnd)
}

// An ending other than completed is never continued, whatever a check asks.
func TestTurnGate_OtherEndingsNeverContinue(t *testing.T) {
	t.Parallel()

	t.Run("tool stop", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{callTool("halt", 1_000)}}
		check := &fakeGateCheck{name: "fake", reason: "keep going"}
		sa := newCompactionAgent(env, model, nil, false, &haltTool{recordingTool{name: "halt"}})
		sa.gateChecks = []gateCheck{check}
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Empty(t, gateRows(t, env, sess.ID))
		require.Equal(t, []message.TurnEnd{message.TurnEndToolStop}, turnEnds(t, env, sess.ID))
		require.Equal(t, message.TurnEndToolStop, check.recorded()[0].TurnEnd)
	})

	t.Run("step limit", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}
		check := &fakeGateCheck{name: "fake", reason: "keep going"}
		sa := newGateAgent(t, env, model, check)
		sa.limits = &runLimits{}
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)
		sa.limits.set(sess.ID, RunLimits{MaxSteps: 1})

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Empty(t, gateRows(t, env, sess.ID))
		require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
		require.Equal(t, message.TurnEndStepLimit, check.recorded()[0].TurnEnd)
	})

	t.Run("loop guard", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &stepRecordingModel{toolSteps: 100}
		check := &fakeGateCheck{name: "fake", reason: "keep going"}
		sa := testSessionAgent(env, model, model, "system", &probeTool{output: "same"}).(*sessionAgent)
		sa.gateChecks = []gateCheck{check}
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go", MaxOutputTokens: 1000})
		require.NoError(t, err)

		require.Empty(t, gateRows(t, env, sess.ID))
		require.Equal(t, []message.TurnEnd{message.TurnEndLoopGuard}, turnEnds(t, env, sess.ID))
		require.Equal(t, message.TurnEndLoopGuard, check.recorded()[0].TurnEnd)
	})
}

// A continued prompt is one prompt: the continuation does not fire
// TurnStart again, as Claude Code does not fire UserPromptSubmit for a
// Stop continuation.
func TestTurnGate_TurnStartFiresOnce(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	sa := newGateAgent(t, env, model, check)
	read := recordingHooks(t, sa, turnEvents...)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	var starts int
	for _, line := range read() {
		if line == string(hooks.EventTurnStart) {
			starts++
		}
	}
	require.Equal(t, 1, starts, "TurnStart fires once for the whole prompt")
}

// The continuation carries the run's RunID, so a caller blocks on one
// terminal event however many runs the prompt took.
func TestTurnGate_OneRunCompletePerRunID(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	sa, broker := newGateRunCompleteAgent(t, env, model, check)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := broker.Subscribe(ctx)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-1", Prompt: "go"})
	require.NoError(t, err)

	first := <-events
	require.Equal(t, "run-1", first.Payload.RunID)
	require.Equal(t, "fixed", first.Payload.Text, "the terminal event carries the continuation's reply")
	select {
	case extra := <-events:
		t.Fatalf("a second RunComplete arrived for RunID %q", extra.Payload.RunID)
	case <-time.After(200 * time.Millisecond):
	}
}

// rowKinds names each row of a session in log order, so a test can pin
// what a turn wrote.
func rowKinds(t *testing.T, env fakeEnv, sessionID string) []string {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	kinds := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		switch {
		case msg.IsSummaryMessage:
			kinds = append(kinds, "summary")
		case msg.Role == message.User && strings.HasPrefix(msg.Content().Text, gateOpenTag):
			kinds = append(kinds, "gate")
		case msg.Role == message.User:
			kinds = append(kinds, "prompt")
		case msg.Role == message.Assistant:
			kinds = append(kinds, "assistant:"+msg.Content().Text)
		default:
			kinds = append(kinds, "other")
		}
	}
	return kinds
}

// The gate puts off the threshold compaction, so a continued turn is
// summed up after the work it is asked to check, not before it. The
// control shows the same turn does compact without a continuation.
func TestTurnGate_PutsOffTheThresholdCompaction(t *testing.T) {
	t.Parallel()
	// 150k of a 200k window is above the default threshold of half.
	const overHalf = 150_000
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000)

	t.Run("the control turn compacts", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{
			answer("done", fantasy.FinishReasonStop, overHalf),
			summary,
		}}
		sa := newGateAgent(t, env, model)
		sess, err := env.sessions.Create(t.Context(), "gate control")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Equal(t, []string{"prompt", "assistant:done", "summary"},
			rowKinds(t, env, sess.ID))
	})

	t.Run("a continued turn compacts after the continuation", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		check := &fakeGateCheck{name: "fake", reason: "check the work"}
		model := &scriptedModel{script: []scriptedCall{
			answer("done", fantasy.FinishReasonStop, overHalf),
			answer("fixed", fantasy.FinishReasonStop, overHalf),
			summary,
		}}
		sa := newGateAgent(t, env, model, check)
		sess, err := env.sessions.Create(t.Context(), "gate")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		require.Equal(t, []string{"prompt", "assistant:done", "gate", "assistant:fixed", "summary"},
			rowKinds(t, env, sess.ID), "the continuation's own ending runs the threshold check")
	})
}

// A prompt with no hooks and the completion check off behaves exactly as it
// did before the gate existed: same rows, same events, one run.
func TestTurnGate_NoChecksChangesNothing(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	sa, broker := newGateRunCompleteAgent(t, env, model)
	read := recordingHooks(t, sa, turnEvents...)
	sess, err := env.sessions.Create(t.Context(), "gate")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := broker.Subscribe(ctx)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-1", Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 1, model.callCount())
	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, []string{"TurnStart", "TurnEnd done"}, read())
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var rows []string
	for _, msg := range msgs {
		rows = append(rows, fmt.Sprintf("%s %q hidden=%t end=%s", msg.Role, msg.Content().Text, msg.Content().Hidden, msg.FinishPart().TurnEnd))
	}
	require.Equal(t, []string{
		`user "go" hidden=false end=`,
		`assistant "done" hidden=false end=completed`,
	}, rows)
	event := <-events
	require.Equal(t, notify.RunComplete{SessionID: sess.ID, RunID: "run-1", Text: "done", Steps: 1, MessageID: msgs[1].ID}, event.Payload)
}

// A prompt typed after the gate decides to continue is queued behind the
// turn, not started on its own, so the pending continuation is what gets
// dropped and the user's prompt runs next.
func TestTurnGate_PromptArrivingDuringTheHandoffIsQueued(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	inner := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("typed answer", fantasy.FinishReasonStop, 1_000),
		answer("continuation", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "keep going", want: "done"}
	sa := newGateAgent(t, env, inner, check)
	sess, err := env.sessions.Create(t.Context(), "window")
	require.NoError(t, err)

	reached, release := make(chan struct{}), make(chan struct{})
	sa.beforeHandoff = func() {
		close(reached)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		done <- runErr
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached the hand-off")
	}
	// The prompt typed here meets a busy session, so it queues.
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "typed", RunID: "run-typed"})
	require.NoError(t, err)
	close(release)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
	}

	require.Empty(t, gateRows(t, env, sess.ID), "the queued prompt drops the continuation")
	require.Equal(t, "typed answer", finalAssistantText(t, env, sess.ID))
	require.Equal(t, 2, inner.callCount(), "the continuation never ran")
}

// A cancel that arrives after the gate decides is not lost: the turn ends
// with the reply it has, without a continuation.
func TestTurnGate_CancelAfterTheGateDecides(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().MCP["signal"] = config.MCPConfig{ChannelReply: &config.MCPChannelReply{
		User: &config.MCPChannelReplyRoute{Tool: "send_message_to_user", TargetParam: "user_id"},
	}}
	inner := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	sa, broker := newGateRunCompleteAgent(t, env, inner, check)
	sa.cfg = cfg
	var replies []string
	sa.replyToolRun = func(_ context.Context, _ *config.ConfigStore, _, tool, input string) (mcp.ToolResult, error) {
		replies = append(replies, tool+" "+input)
		return mcp.ToolResult{Type: "text", Content: "sent"}, nil
	}
	sess, err := env.sessions.Create(t.Context(), "window")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := broker.Subscribe(ctx)
	reached, release := make(chan struct{}), make(chan struct{})
	sa.beforeHandoff = func() {
		close(reached)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			SessionID:   sess.ID,
			RunID:       "run-1",
			Prompt:      "go",
			Channel:     "signal",
			channelMeta: map[string]string{"sender": "+15551234567"},
		})
		done <- runErr
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached the hand-off")
	}
	sa.Cancel(sess.ID)
	close(release)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish after the cancel")
	}

	require.Empty(t, gateRows(t, env, sess.ID), "the cancel drops the continuation")
	require.Len(t, replies, 1, "the reply is sent exactly once")
	require.Contains(t, replies[0], "done")
	require.Equal(t, 1, inner.callCount())
	require.Equal(t, "run-1", (<-events).Payload.RunID)
}

// assistantTexts lists the text of every assistant row that is not a
// compaction summary, in log order.
func assistantTexts(t *testing.T, env fakeEnv, sessionID string) []string {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var texts []string
	for _, msg := range msgs {
		if msg.Role != message.Assistant || msg.IsSummaryMessage {
			continue
		}
		texts = append(texts, msg.Content().Text)
	}
	return texts
}

// droppedContinuation arranges the drop: a blocking check, a prompt typed
// while it runs, and the values the caller waits on.
type dropSetup struct {
	check  *fakeGateCheck
	done   chan error
	finish func()
}

// startDroppedContinuation runs the prompt in the background and queues a
// user prompt while the check holds, then releases the check.
func startDroppedContinuation(t *testing.T, sa *sessionAgent, env fakeEnv, sessionID string) *dropSetup {
	t.Helper()
	setup := &dropSetup{
		check: &fakeGateCheck{
			name:   "fake",
			reason: "keep going",
			// Only the first turn asks, so the typed prompt's own
			// ending leaves no row behind.
			want:    "done",
			block:   make(chan struct{}),
			entered: make(chan struct{}, 1),
		},
		done: make(chan error, 1),
	}
	sa.gateChecks = []gateCheck{setup.check}
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sessionID, Prompt: "go"})
		setup.done <- runErr
	}()
	select {
	case <-setup.check.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate never reached the check")
	}
	sa.enqueueCall(SessionAgentCall{SessionID: sessionID, Prompt: "typed", RunID: "run-typed"})
	setup.finish = func() {
		close(setup.check.block)
		select {
		case runErr := <-setup.done:
			require.NoError(t, runErr)
		case <-time.After(10 * time.Second):
			t.Fatal("the run did not finish")
		}
	}
	return setup
}

// A dropped continuation still ends its turn: the stale list note is
// written, because this run turned out to be the prompt's last.
func TestTurnGate_DroppedContinuationWritesTheStaleNote(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	script := make([]scriptedCall, 0, 8)
	for range 5 {
		script = append(script, callTool(tools.BashToolName, 1_000))
	}
	script = append(script,
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("typed answer", fantasy.FinishReasonStop, 1_000),
	)
	sa := newCompactionAgent(env, &scriptedModel{script: script}, nil, false, &recordingTool{name: tools.BashToolName})
	sess, err := env.sessions.Create(t.Context(), "dropped note")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(t.Context(), sess.ID, []session.Todo{
		{Content: "Deploy it", Status: session.TodoStatusInProgress},
	}))

	startDroppedContinuation(t, sa, env, sess.ID).finish()

	require.Empty(t, gateRows(t, env, sess.ID))
	texts := assistantTexts(t, env, sess.ID)
	require.NotEmpty(t, texts)
	require.Contains(t, strings.Join(texts, "\n"),
		`Todo list still shows "Deploy it" as unfinished`,
		"the dropped turn still ends with the note")
}

// A dropped continuation runs the threshold compaction it put off, so a long
// turn is still summarized.
func TestTurnGate_DroppedContinuationCompactsTheTurn(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	// 150k of a 200k window is above the default threshold of half.
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 150_000),
		answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000),
		answer("typed answer", fantasy.FinishReasonStop, 1_000),
	}}
	sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	sess, err := env.sessions.Create(t.Context(), "dropped compaction")
	require.NoError(t, err)

	startDroppedContinuation(t, sa, env, sess.ID).finish()

	require.Empty(t, gateRows(t, env, sess.ID))
	require.Contains(t, rowKinds(t, env, sess.ID), "summary",
		"the dropped continuation still compacts the turn")
	require.Equal(t, "typed answer", finalAssistantText(t, env, sess.ID))
}

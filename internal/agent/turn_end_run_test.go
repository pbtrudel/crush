package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// scriptedCall is one scripted model answer: stream parts, or an error
// returned instead of a stream.
type scriptedCall struct {
	parts []fantasy.StreamPart
	err   error
}

// scriptedModel answers the agent's calls in order and repeats the last
// answer once the script runs out. Calls without tools (title generation)
// get a plain answer and do not use up the script; agent steps and
// compactions both carry the session's tools.
type scriptedModel struct {
	finishStreamModel
	mu     sync.Mutex
	script []scriptedCall
	calls  int
}

// callCount reports how many of the agent's calls used the script.
func (m *scriptedModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *scriptedModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) == 0 {
		return replayStreamParts(fusionTextParts("Title")), nil
	}
	m.mu.Lock()
	next := m.script[min(m.calls, len(m.script)-1)]
	m.calls++
	m.mu.Unlock()
	if next.err != nil {
		return nil, next.err
	}
	return replayStreamParts(next.parts), nil
}

func answer(text string, finish fantasy.FinishReason, inputTokens int64) scriptedCall {
	parts := []fantasy.StreamPart{}
	if text != "" {
		parts = append(parts,
			fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text"},
			fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text", Delta: text},
			fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text"},
		)
	}
	parts = append(parts, fantasy.StreamPart{
		Type:         fantasy.StreamPartTypeFinish,
		FinishReason: finish,
		Usage:        fantasy.Usage{InputTokens: inputTokens, OutputTokens: 10, TotalTokens: inputTokens + 10},
	})
	return scriptedCall{parts: parts}
}

func callTool(name string, inputTokens int64) scriptedCall {
	return scriptedCall{parts: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeToolCall, ID: "call-1", ToolCallName: name, ToolCallInput: "{}"},
		{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: inputTokens, OutputTokens: 10, TotalTokens: inputTokens + 10},
		},
	}}
}

func failWith(err error) scriptedCall { return scriptedCall{err: err} }

// blockingPreCompact refuses every compaction, like a hook that exits 2.
var blockingPreCompact = map[string][]config.HookConfig{
	hooks.EventPreCompact: {{Command: `echo "not now" >&2; exit 2`}},
}

// haltTool answers like a denied permission: the result ends the turn.
type haltTool struct{ recordingTool }

func (t *haltTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.ToolResponse{Type: "text", Content: "permission denied", IsError: true, StopTurn: true}, nil
}

// turnEnds lists the turn_end of every assistant message that has one, in
// order, and checks that no compaction summary carries one.
func turnEnds(t *testing.T, env fakeEnv, sessionID string) []message.TurnEnd {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var ends []message.TurnEnd
	for _, msg := range msgs {
		finish := msg.FinishPart()
		if msg.Role != message.Assistant || finish == nil {
			continue
		}
		if msg.IsSummaryMessage {
			require.Empty(t, finish.TurnEnd, "a compaction summary never carries turn_end")
			continue
		}
		if finish.TurnEnd != "" {
			ends = append(ends, finish.TurnEnd)
		}
	}
	return ends
}

// Window 200,000 tokens: the mid-turn guard trips when 40,000 or fewer
// remain, and the threshold compacts a finished turn at 100,000.
func TestTurnEnd_Runs(t *testing.T) {
	const (
		small    = 1_000
		atGuard  = 190_000
		overHalf = 150_000
	)
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, small)
	tests := []struct {
		name        string
		script      []scriptedCall
		tool        fantasy.AgentTool
		wantErr     bool
		want        []message.TurnEnd
		wantTrigger string
	}{
		{name: "completed", script: []scriptedCall{answer("done", fantasy.FinishReasonStop, small)}, want: []message.TurnEnd{message.TurnEndCompleted}},
		{name: "empty", script: []scriptedCall{answer("", fantasy.FinishReasonStop, small)}, want: []message.TurnEnd{message.TurnEndEmpty}},
		{name: "max tokens", script: []scriptedCall{answer("partial", fantasy.FinishReasonLength, small)}, want: []message.TurnEnd{message.TurnEndMaxTokens}},
		{name: "content filter", script: []scriptedCall{answer("", fantasy.FinishReasonContentFilter, small)}, want: []message.TurnEnd{message.TurnEndContentFilter}},
		{name: "cut short by the provider", script: []scriptedCall{answer("partial", fantasy.FinishReasonError, small)}, want: []message.TurnEnd{message.TurnEndProviderError}},
		{name: "repetition", script: []scriptedCall{answer(strings.Repeat("a", 600), fantasy.FinishReasonStop, small)}, wantErr: true, want: []message.TurnEnd{message.TurnEndRepetitionGuard}},
		{name: "canceled", script: []scriptedCall{failWith(context.Canceled)}, wantErr: true, want: []message.TurnEnd{message.TurnEndCanceled}},
		{name: "request timeout", script: []scriptedCall{failWith(&requestTimeoutError{timeout: time.Second, cause: context.DeadlineExceeded})}, wantErr: true, want: []message.TurnEnd{message.TurnEndRequestTimeout}},
		{name: "provider error", script: []scriptedCall{failWith(&fantasy.ProviderError{StatusCode: 400, Message: "bad request"})}, wantErr: true, want: []message.TurnEnd{message.TurnEndProviderError}},
		{name: "other error", script: []scriptedCall{failWith(errors.New("boom"))}, wantErr: true, want: []message.TurnEnd{message.TurnEndError}},
		{name: "tool stop", script: []scriptedCall{callTool("halt", small)}, tool: &haltTool{recordingTool{name: "halt"}}, want: []message.TurnEnd{message.TurnEndToolStop}},
		{
			name:        "context guard, then the continuation completes",
			script:      []scriptedCall{callTool("alpha", atGuard), summary, answer("done", fantasy.FinishReasonStop, small)},
			want:        []message.TurnEnd{message.TurnEndContextGuard, message.TurnEndCompleted},
			wantTrigger: "guard",
		},
		{
			name:        "context overflow, then the continuation completes",
			script:      []scriptedCall{failWith(&fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true}), summary, answer("done", fantasy.FinishReasonStop, small)},
			want:        []message.TurnEnd{message.TurnEndContextOverflow, message.TurnEndCompleted},
			wantTrigger: "overflow",
		},
		{
			name:    "required compaction failed",
			script:  []scriptedCall{callTool("alpha", atGuard), failWith(errors.New("summary failed"))},
			wantErr: true,
			want:    []message.TurnEnd{message.TurnEndCompactionFailed},
		},
		{
			// The model finished; the compaction afterwards fails and Run
			// reports it, but the run's ending is unchanged.
			name:    "compaction after a completed turn failed",
			script:  []scriptedCall{answer("done", fantasy.FinishReasonStop, overHalf), failWith(errors.New("summary failed"))},
			wantErr: true,
			want:    []message.TurnEnd{message.TurnEndCompleted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t)
			tool := tt.tool
			if tool == nil {
				tool = &recordingTool{name: "alpha"}
			}
			agent := newCompactionAgent(env, &scriptedModel{script: tt.script}, nil, false, tool)
			sess, err := env.sessions.Create(t.Context(), tt.name)
			require.NoError(t, err)

			_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.want, turnEnds(t, env, sess.ID))
			if tt.wantTrigger != "" {
				require.Equal(t, tt.wantTrigger, summaryTrigger(t, env, sess.ID))
			}
		})
	}
}

// A compaction the PreCompact hook blocks ends the turn. The call is not
// requeued, so a guard-tripping step costs one provider request instead of
// looping on a context that never shrank.
func TestBlockedCompactionEndsTheTurn(t *testing.T) {
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000)}}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	dir := t.TempDir()
	agent.hooks = hooks.NewRunner(hooks.Static(blockingPreCompact), dir, dir)
	sess, err := env.sessions.Create(t.Context(), "blocked compaction")
	require.NoError(t, err)

	// The deadline is a hang guard, not a budget: the turn takes a few
	// milliseconds, and only a wedged run makes this fire.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, err = agent.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 1, model.callCount(), "the run must not be requeued after a blocked compaction")
	require.Equal(t, []message.TurnEnd{message.TurnEndCompactionBlocked}, turnEnds(t, env, sess.ID))
}

// A blocked compaction ends the turn with the work unfinished, so a channel
// peer must be told instead of waiting for a reply that never comes.
func TestBlockedCompactionAnswersTheChannel(t *testing.T) {
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000)}}
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().MCP["signal"] = config.MCPConfig{
		ChannelReply: &config.MCPChannelReply{
			User: &config.MCPChannelReplyRoute{Tool: "send_message_to_user", TargetParam: "user_id"},
		},
	}
	agent := newCompactionAgent(env, model, cfg, false, &recordingTool{name: "alpha"})
	dir := t.TempDir()
	agent.hooks = hooks.NewRunner(hooks.Static(blockingPreCompact), dir, dir)
	var replies []string
	agent.replyToolRun = func(_ context.Context, _ *config.ConfigStore, _, tool, input string) (mcp.ToolResult, error) {
		replies = append(replies, tool+" "+input)
		return mcp.ToolResult{Type: "text", Content: "sent"}, nil
	}
	sess, err := env.sessions.Create(t.Context(), "blocked compaction channel")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{
		SessionID:   sess.ID,
		Prompt:      "go",
		Channel:     "signal",
		channelMeta: map[string]string{"sender": "+15551234567"},
	})
	require.NoError(t, err)

	require.Len(t, replies, 1, "a blocked compaction must answer the channel exactly once")
	require.Contains(t, replies[0], "send_message_to_user")
	require.Contains(t, replies[0], "Something went wrong while handling your message")
}

// Twenty identical tool interactions trip the loop guard.
func TestTurnEnd_LoopGuard(t *testing.T) {
	env := testEnv(t)
	model := &stepRecordingModel{toolSteps: 100}
	agent := testSessionAgent(env, model, model, "system", &probeTool{output: "same"})
	sess, err := env.sessions.Create(t.Context(), "loop")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go", MaxOutputTokens: 1000})
	require.NoError(t, err)

	require.Equal(t, []message.TurnEnd{message.TurnEndLoopGuard}, turnEnds(t, env, sess.ID))
}

// The turn-end stamp is log metadata: a session that carries it must
// produce the provider request it produced before it existed.
func TestTurnEnd_NeverReachesTheProvider(t *testing.T) {
	sa, _ := newStreamTestAgent(t)
	transcript := func(end message.TurnEnd) []message.Message {
		return []message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
			{ID: "a1", Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: 1, TurnEnd: end},
			}},
		}
	}

	stamped, _ := sa.preparePrompt(transcript(message.TurnEndContextGuard), false, false)
	plain, _ := sa.preparePrompt(transcript(""), false, false)
	require.Equal(t, plain, stamped)
}

// TestTurnEnd_ProviderDeadlineIsATimeout pins that a provider returning a raw
// deadline error while the run context is still alive ends the turn as the
// timeout it is, with the configured budget named, instead of a provider
// error the user cannot tell from a fault.
func TestTurnEnd_ProviderDeadlineIsATimeout(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{
		failWith(fmt.Errorf("stream: %w", context.DeadlineExceeded)),
	}}, nil, false, &recordingTool{name: "alpha"})
	sess, err := env.sessions.Create(t.Context(), "provider deadline")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.Error(t, err)

	require.Equal(t, []message.TurnEnd{message.TurnEndRequestTimeout}, turnEnds(t, env, sess.ID))

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, "Request timed out", finish.Message)
	require.Contains(t, finish.Details, "The provider reported a deadline")
}

// cancelingModel cancels the run's own context and then reports a deadline,
// which is what a caller's deadline looks like when the provider echoes it.
type cancelingModel struct {
	finishStreamModel
	cancel context.CancelFunc
}

func (m *cancelingModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) == 0 {
		return replayStreamParts(fusionTextParts("Title")), nil
	}
	m.cancel()
	return nil, fmt.Errorf("stream: %w", context.DeadlineExceeded)
}

// TestTurnEnd_ProviderDeadlineDuringCancelIsNotATimeout pins that a deadline
// error arriving after the run's own context ended stays a cancel: Crush
// cannot tell a caller's deadline from a provider's, so it claims neither.
func TestTurnEnd_ProviderDeadlineDuringCancelIsNotATimeout(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	agent := newCompactionAgent(env, &cancelingModel{cancel: cancel}, nil, false, &recordingTool{name: "alpha"})
	sess, err := env.sessions.Create(t.Context(), "canceled deadline")
	require.NoError(t, err)

	_, err = agent.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.Error(t, err)

	require.Equal(t, []message.TurnEnd{message.TurnEndCanceled}, turnEnds(t, env, sess.ID))

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, "Provider Error", finish.Message)
	require.NotContains(t, finish.Details, "request deadline")
}

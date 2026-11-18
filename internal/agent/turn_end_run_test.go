package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
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
	blockCompaction := map[string][]config.HookConfig{
		hooks.EventPreCompact: {{Command: `echo "not now" >&2; exit 2`}},
	}
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, small)
	tests := []struct {
		name        string
		script      []scriptedCall
		tool        fantasy.AgentTool
		hooks       map[string][]config.HookConfig
		wantErr     bool
		want        []message.TurnEnd
		wantTrigger string
	}{
		{name: "completed", script: []scriptedCall{answer("done", fantasy.FinishReasonStop, small)}, want: []message.TurnEnd{message.TurnEndCompleted}},
		{name: "empty", script: []scriptedCall{answer("", fantasy.FinishReasonStop, small)}, want: []message.TurnEnd{message.TurnEndEmpty}},
		{name: "max tokens", script: []scriptedCall{answer("partial", fantasy.FinishReasonLength, small)}, want: []message.TurnEnd{message.TurnEndMaxTokens}},
		{name: "content filter", script: []scriptedCall{answer("", fantasy.FinishReasonContentFilter, small)}, want: []message.TurnEnd{message.TurnEndContentFilter}},
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
			// A blocked compaction still requeues the prompt today; the
			// continuation starts from a small context and completes.
			name:   "required compaction blocked",
			script: []scriptedCall{callTool("alpha", atGuard), answer("done", fantasy.FinishReasonStop, small)},
			hooks:  blockCompaction,
			want:   []message.TurnEnd{message.TurnEndCompactionBlocked, message.TurnEndCompleted},
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
			if tt.hooks != nil {
				dir := t.TempDir()
				agent.hooks = hooks.NewRunner(hooks.Static(tt.hooks), dir, dir)
			}
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

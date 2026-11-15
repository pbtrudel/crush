package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestWithInjections(t *testing.T) {
	t.Parallel()

	base := []fantasy.Message{
		fantasy.NewUserMessage("b0"),
		fantasy.NewUserMessage("b1"),
		fantasy.NewUserMessage("b2"),
		fantasy.NewUserMessage("b3"),
	}
	group := func(at int, texts ...string) stepInjection {
		injection := stepInjection{at: at}
		for _, text := range texts {
			injection.messages = append(injection.messages, fantasy.NewUserMessage(text))
		}
		return injection
	}

	tests := []struct {
		name       string
		injections []stepInjection
		want       []string
	}{
		{
			name: "none",
			want: []string{"user|text:b0", "user|text:b1", "user|text:b2", "user|text:b3"},
		},
		{
			name:       "one group in the middle",
			injections: []stepInjection{group(2, "x")},
			want:       []string{"user|text:b0", "user|text:b1", "user|text:x", "user|text:b2", "user|text:b3"},
		},
		{
			name:       "two groups",
			injections: []stepInjection{group(2, "x", "y"), group(4, "z")},
			want: []string{
				"user|text:b0", "user|text:b1", "user|text:x", "user|text:y",
				"user|text:b2", "user|text:b3", "user|text:z",
			},
		},
		{
			name:       "position past the end is appended",
			injections: []stepInjection{group(9, "x")},
			want:       []string{"user|text:b0", "user|text:b1", "user|text:b2", "user|text:b3", "user|text:x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, promptShape(withInjections(slices.Clone(base), tt.injections)))
		})
	}
}

// stepRecordingModel calls the "probe" tool on its first toolSteps agent
// steps, then ends the turn, and records the prompt of every agent step.
// Side calls (title generation) carry no tools and are not recorded.
type stepRecordingModel struct {
	fakeLanguageModel
	mu        sync.Mutex
	toolSteps int
	prompts   []fantasy.Prompt
}

func (m *stepRecordingModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) == 0 {
		return func(yield func(fantasy.StreamPart) bool) {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
		}, nil
	}
	m.mu.Lock()
	step := len(m.prompts)
	m.prompts = append(m.prompts, slices.Clone(call.Prompt))
	m.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		if step < m.toolSteps {
			if !yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            fmt.Sprintf("call-%d", step+1),
				ToolCallName:  "probe",
				ToolCallInput: "{}",
			}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

// probeTool answers with output and calls onRun with the 1-based run count.
type probeTool struct {
	output string
	onRun  func(run int)
	runs   int
}

func (t *probeTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: "probe"} }

func (t *probeTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	t.runs++
	if t.onRun != nil {
		t.onRun(t.runs)
	}
	return fantasy.NewTextResponse(t.output), nil
}

func (t *probeTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (t *probeTool) SetProviderOptions(fantasy.ProviderOptions) {}

// promptShape renders messages as role-tagged text, so two prompts can be
// compared without their provider options.
func promptShape(messages []fantasy.Message) []string {
	shapes := make([]string, 0, len(messages))
	for _, msg := range messages {
		var shape strings.Builder
		shape.WriteString(string(msg.Role))
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				shape.WriteString("|text:" + text.Text)
			} else if call, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				shape.WriteString("|call:" + call.ToolCallID)
			} else if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				output, _ := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output)
				shape.WriteString("|result:" + result.ToolCallID + ":" + output.Text)
			}
		}
		shapes = append(shapes, shape.String())
	}
	return shapes
}

func TestMidTurnMessagesStayInLaterSteps(t *testing.T) {
	env := testEnv(t)
	model := &stepRecordingModel{toolSteps: 2}
	const notice = "<background_job_notice>Background job 001 finished.</background_job_notice>"

	var agent SessionAgent
	var sessionID string
	probe := &probeTool{output: "ok", onRun: func(run int) {
		if run == 1 {
			agent.EnqueueNotice(sessionID, notice)
		}
	}}
	agent = testSessionAgent(env, model, model, "system", probe)

	sess, err := env.sessions.Create(t.Context(), "mid-turn")
	require.NoError(t, err)
	sessionID = sess.ID

	_, err = agent.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.prompts, 3)

	second, third := promptShape(model.prompts[1]), promptShape(model.prompts[2])
	at := slices.Index(second, "user|text:"+notice)
	require.NotEqual(t, -1, at, "the step after the notice arrived must carry it")
	require.Equal(t, at, slices.Index(third, "user|text:"+notice), "a later step must keep the notice where it was")
	require.Equal(t, second, third[:len(second)], "each step must extend the previous request")

	// The next turn rebuilds its prompt from the session log, and that
	// rebuild is what the provider's cache is matched against.
	sa := agent.(*sessionAgent)
	stored, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	msgs, err := sa.getSessionMessages(t.Context(), stored)
	require.NoError(t, err)
	replay, _ := sa.preparePrompt(msgs, false, false)
	require.Equal(t, third[1:], promptShape(replay), "the replayed session must match the last request of the turn")
}

func TestNoticeGroupsKeepArrivalOrderAcrossSteps(t *testing.T) {
	env := testEnv(t)
	model := &stepRecordingModel{toolSteps: 2}
	const first = "<background_job_notice>Background job 001 finished.</background_job_notice>"
	const second = "<background_job_notice>Background job 002 finished.</background_job_notice>"

	var agent SessionAgent
	var sessionID string
	probe := &probeTool{output: "ok", onRun: func(run int) {
		agent.EnqueueNotice(sessionID, []string{first, second}[run-1])
	}}
	agent = testSessionAgent(env, model, model, "system", probe)

	sess, err := env.sessions.Create(t.Context(), "two-groups")
	require.NoError(t, err)
	sessionID = sess.ID

	_, err = agent.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.prompts, 3)

	third := promptShape(model.prompts[2])
	firstAt := slices.Index(third, "user|text:"+first)
	secondAt := slices.Index(third, "user|text:"+second)
	require.NotEqual(t, -1, firstAt, "the first notice must stay in the last step")
	require.NotEqual(t, -1, secondAt, "the second notice must be in the last step")
	require.Less(t, firstAt, secondAt, "notices must keep their arrival order")
	require.Equal(t, firstAt+3, secondAt, "the second notice follows the assistant and tool messages of the step it arrived in")

	sa := agent.(*sessionAgent)
	stored, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	msgs, err := sa.getSessionMessages(t.Context(), stored)
	require.NoError(t, err)
	replay, _ := sa.preparePrompt(msgs, false, false)
	require.Equal(t, third[1:], promptShape(replay), "the replayed session must match the last request of the turn")
}

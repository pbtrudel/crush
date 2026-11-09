package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// recordingLanguageModel records every call it receives so tests can assert
// on the request the ephemeral path builds.
type recordingLanguageModel struct {
	mu      sync.Mutex
	calls   []fantasy.Call
	reply   string
	replies []string
	finish  fantasy.FinishReason
}

func (m *recordingLanguageModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{}, nil
}

func (m *recordingLanguageModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	reply := m.reply
	if len(m.replies) > 0 {
		reply = m.replies[min(len(m.calls)-1, len(m.replies)-1)]
	}
	finish := m.finish
	m.mu.Unlock()
	if finish == "" {
		finish = fantasy.FinishReasonStop
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if reply == "" {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: finish})
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "0"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "0", Delta: reply})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "0"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *recordingLanguageModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *recordingLanguageModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *recordingLanguageModel) Provider() string { return "recording" }
func (m *recordingLanguageModel) Model() string    { return "recording-model" }

func (m *recordingLanguageModel) lastCall(t *testing.T) fantasy.Call {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotEmpty(t, m.calls, "the model must have been called")
	return m.calls[len(m.calls)-1]
}

func (m *recordingLanguageModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// promptText joins the text parts of a call's prompt so a test can assert on
// what the model was asked.
func promptText(t *testing.T, call fantasy.Call) string {
	t.Helper()
	var joined strings.Builder
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				joined.WriteString(text.Text)
				joined.WriteString("\n")
			}
		}
	}
	return joined.String()
}

// TestEphemeralAnswersWithSideQuestionPrompt pins the fix for side questions
// that came back empty: the ephemeral call used to send the coding-agent
// system prompt, which tells the model to go read files and run commands,
// while registering no tools. Models answered with a promise to look around
// (or with nothing at all) instead of answering.
func TestEphemeralAnswersWithSideQuestionPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &recordingLanguageModel{reply: "You asked about the pooling option."}
	agent := testSessionAgent(env, model, model, "CODER PROMPT: use your tools to inspect the workspace")

	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	res, err := agent.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "what was the pooling option?",
		Ephemeral: true,
	})
	require.NoError(t, err)
	require.Equal(t, "You asked about the pooling option.", subAgentOutput(res))

	call := model.lastCall(t)
	require.Empty(t, call.Tools, "a side question must not offer tools")

	require.NotEmpty(t, call.Prompt)
	system, ok := fantasy.AsMessagePart[fantasy.TextPart](call.Prompt[0].Content[0])
	require.True(t, ok, "the first message must be the system prompt")
	require.NotContains(t, system.Text, "CODER PROMPT")
	require.Contains(t, strings.ToLower(system.Text), "side question")
}

// TestEphemeralKeepsConversationHistory guards the reason side questions are
// useful: the model still sees the conversation it is being asked about.
func TestEphemeralKeepsConversationHistory(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &recordingLanguageModel{reply: "answer"}
	agent := testSessionAgent(env, model, model, "system prompt")

	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "we picked the pooled connection"}},
	})
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "what was the pooling option?",
		Ephemeral: true,
	})
	require.NoError(t, err)

	call := model.lastCall(t)
	joined := promptText(t, call)
	require.Contains(t, joined, "we picked the pooled connection")
	require.Contains(t, joined, "what was the pooling option?")
}

// TestEphemeralRetriesWhenTheProviderAnswersWithNothing pins the failure
// behind /btw on long agentic sessions: the history is made of tool turns,
// so the model answers with a tool call; with tools disabled the provider
// drops the call and reports a normal stop with no content. The run must
// retry once with an explicit answer-only instruction instead of failing.
func TestEphemeralRetriesWhenTheProviderAnswersWithNothing(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &recordingLanguageModel{replies: []string{"", "the answer"}}
	agent := testSessionAgent(env, model, model, "system prompt")

	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	res, err := agent.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "what is the state of the bump?",
		Ephemeral: true,
	})
	require.NoError(t, err)
	require.Equal(t, "the answer", subAgentOutput(res))
	require.Equal(t, 2, model.callCount(), "an empty answer must be retried once")

	retry := promptText(t, model.lastCall(t))
	require.Contains(t, retry, "what is the state of the bump?")
	require.Contains(t, retry, "Do not call any tools.")
}

// TestEphemeralReportsWhyAnAnswerIsEmpty pins the failure mode behind the
// "(the model returned no answer)" card: when the provider ends the response
// without any text, the ephemeral run used to return an empty answer and no
// error, so callers could not tell a silent provider stop from a real answer.
// The run must surface the finish reason instead.
func TestEphemeralReportsWhyAnAnswerIsEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		finish fantasy.FinishReason
		want   string
	}{
		{"provider stopped", fantasy.FinishReasonError, "finish_reason=error"},
		{"truncated", fantasy.FinishReasonLength, "finish_reason=length"},
		{"empty stop", fantasy.FinishReasonStop, "finish_reason=stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := testEnv(t)
			model := &recordingLanguageModel{finish: tc.finish}
			agent := testSessionAgent(env, model, model, "system prompt")

			sess, err := env.sessions.Create(t.Context(), "New Session")
			require.NoError(t, err)

			res, err := agent.Run(t.Context(), SessionAgentCall{
				SessionID: sess.ID,
				Prompt:    "what is taking so long?",
				Ephemeral: true,
			})
			require.Error(t, err)
			require.Nil(t, res)
			require.Contains(t, err.Error(), tc.want)
			require.Contains(t, err.Error(), "no answer")
			require.Equal(t, 2, model.callCount(), "the empty answer must be retried once")
		})
	}
}

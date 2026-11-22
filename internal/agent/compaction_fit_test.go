package agent

import (
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestCompactionFits pins when a compaction request is allowed to carry the
// history untouched: the context the last request reported, the summary the
// model may write, and the instruction all have to fit in the window.
func TestCompactionFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		window   int64
		context  int64
		output   int64
		expected bool
	}{
		{name: "comfortably fits", window: 200_000, context: 50_000, output: 16_384, expected: true},
		{
			name:     "exactly at the window fits",
			window:   200_000,
			context:  200_000 - 16_384 - compactionRequestReserve,
			output:   16_384,
			expected: true,
		},
		{
			name:     "one token over does not",
			window:   200_000,
			context:  200_000 - 16_384 - compactionRequestReserve + 1,
			output:   16_384,
			expected: false,
		},
		{name: "zero window does not fit", window: 0, context: 1_000, output: 16_384, expected: false},
		{name: "zero context does not fit", window: 200_000, context: 0, output: 16_384, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, compactionFits(tt.window, tt.context, tt.output))
		})
	}
}

// toolResultTextIn returns the text of the first tool result in a recorded
// compaction prompt.
func toolResultTextIn(t *testing.T, prompt fantasy.Prompt) string {
	t.Helper()
	for _, msg := range prompt {
		for _, part := range msg.Content {
			result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}
			output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output)
			if !ok {
				continue
			}
			return output.Text
		}
	}
	t.Fatal("no tool result in the compaction prompt")
	return ""
}

// seedOversizedHistory stores a session whose head carries one oversized tool
// result, followed by enough conversation that the result is not kept in the
// verbatim tail.
func seedOversizedHistory(t *testing.T, env fakeEnv, sessionID string, resultChars int) {
	t.Helper()
	ctx := t.Context()
	create := func(role message.MessageRole, text string) {
		_, err := env.messages.Create(ctx, sessionID, message.CreateMessageParams{
			Role:  role,
			Parts: []message.ContentPart{message.TextContent{Text: text}},
		})
		require.NoError(t, err)
	}

	create(message.User, "start")
	_, err := env.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "c1", Name: "bash", Input: "{}", Finished: true}},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "c1",
			Name:       "bash",
			Content:    strings.Repeat("r", resultChars),
		}},
	})
	require.NoError(t, err)
	for range 4 {
		create(message.Assistant, bigText(2500))
	}
}

// TestSummarize_PrunesOnlyWhenTheRequestCannotFit is the cost guard: pruning
// rewrites tool results the provider has already cached, so a compaction that
// fits must send the conversation byte-identical, even when the mid-turn
// guard is what asked for it. An overflow recovery is the exception: the
// provider has already rejected that request as too large, so the estimate
// that says it fits cannot be trusted.
func TestSummarize_PrunesOnlyWhenTheRequestCannotFit(t *testing.T) {
	const resultChars = 20_000

	run := func(t *testing.T, trigger compactionTrigger, promptTokens int64) string {
		t.Helper()
		env := testEnv(t)
		stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
		agent := newCompactionAgent(env, stub, nil, false)

		ctx := t.Context()
		sess, err := env.sessions.Create(ctx, "test")
		require.NoError(t, err)
		seedOversizedHistory(t, env, sess.ID, resultChars)

		sess.PromptTokens = promptTokens
		_, err = env.sessions.Save(ctx, sess)
		require.NoError(t, err)

		_, err = agent.summarize(ctx, compactionRequest{
			sessionID:  sess.ID,
			largeModel: agent.largeModel.Get(),
			trigger:    trigger,
		})
		require.NoError(t, err)
		require.Len(t, stub.calls, 1)
		return toolResultTextIn(t, stub.calls[0].Prompt)
	}

	t.Run("a guard-triggered request that fits keeps the history intact", func(t *testing.T) {
		// A reported context that leaves the window with room to spare.
		text := run(t, compactionTriggerGuard, 50_000)
		require.Len(t, text, resultChars, "an oversized result must reach the summarizer in full")
		require.False(t, tools.HasSpillNotice(text))
	})

	t.Run("a guard-triggered request that cannot fit prunes", func(t *testing.T) {
		// Past the window once the summary budget and the instruction are
		// reserved.
		text := run(t, compactionTriggerGuard, 195_000)
		require.True(t, tools.HasSpillNotice(text))
		require.Less(t, len(text), resultChars)
	})

	t.Run("an overflow-triggered request that fits still prunes", func(t *testing.T) {
		// The estimate says the request fits, but the provider already
		// rejected it as too large.
		text := run(t, compactionTriggerOverflow, 50_000)
		require.True(t, tools.HasSpillNotice(text))
		require.Less(t, len(text), resultChars)
	})
}

// TestSummarize_PrunesWhenTheContextSizeIsUnknown keeps the old behaviour for
// the sessions that never reported usage: without a size there is no way to
// know the request fits, so it is trimmed.
func TestSummarize_PrunesWhenTheContextSizeIsUnknown(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, nil, false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)
	seedOversizedHistory(t, env, sess.ID, 20_000)

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.Len(t, stub.calls, 1)
	require.True(t, tools.HasSpillNotice(toolResultTextIn(t, stub.calls[0].Prompt)))
}

// newWindowAgent builds a compaction agent whose model has the given context
// window, so a test can cross the compaction threshold with a small history.
func newWindowAgent(env fakeEnv, model fantasy.LanguageModel, window int64, tools ...fantasy.AgentTool) *sessionAgent {
	m := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: window, DefaultMaxTokens: 32_000}}
	return NewSessionAgent(SessionAgentOptions{
		Models:       func() ModelSet { return ModelSet{Large: m, Small: m} },
		SystemPrompt: "SYSTEM PROMPT",
		Sessions:     env.sessions,
		Messages:     env.messages,
		Tools:        tools,
		titles:       env.titles,
	}).(*sessionAgent)
}

// TestSummarizePrunesWhenTheStoredHistoryIsBiggerThanTheReportedUsage is the
// guard for a preflight compaction that decided to run from the assembled
// prompt: the session's reported usage predates the tool result that filled
// the window, so a fits check made from that number alone would skip the
// prune and overflow.
func TestSummarizePrunesWhenTheStoredHistoryIsBiggerThanTheReportedUsage(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}

	// A 20,000 token window leaves about 6,800 tokens for the history once
	// the summary budget and the instruction are reserved.
	agent := newWindowAgent(env, stub, 20_000)

	sess, err := env.sessions.Create(ctx, "preflight")
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "run the suite"}},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{
			ID: "c1", Name: "bash", Input: "{}", Finished: true,
		}},
	})
	require.NoError(t, err)
	// 40,000 characters is about 10,000 tokens: stored after the last
	// reported usage, so the session's own count does not include it.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "c1", Name: "bash", Content: strings.Repeat("r", 40_000),
		}},
	})
	require.NoError(t, err)

	// The reported usage is tiny compared with what the request will carry.
	sess.PromptTokens = 1_000
	_, err = env.sessions.Save(ctx, sess)
	require.NoError(t, err)

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.Len(t, stub.calls, 1)

	require.True(t, tools.HasSpillNotice(toolResultTextIn(t, stub.calls[0].Prompt)),
		"the oversized result must be pruned so the request fits")
}

// TestSummarize_PrunesAtTheFitBoundary pins the line itself: the last
// reported context the request still fits sends the history untouched, and
// one token past it prunes.
func TestSummarize_PrunesAtTheFitBoundary(t *testing.T) {
	const resultChars = 20_000

	// run summarizes the seeded history with a reported context that many
	// tokens past the line where the request stops fitting.
	run := func(t *testing.T, pastFitLine int64) string {
		t.Helper()
		env := testEnv(t)
		stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
		agent := newCompactionAgent(env, stub, nil, false)

		model := agent.largeModel.Get()
		window := effectiveContextWindow(model)
		output := compactionMaxOutputTokens(model)
		fitLine := window - output - compactionRequestReserve
		require.True(t, compactionFits(window, fitLine, output), "the fit line itself must fit")
		require.False(t, compactionFits(window, fitLine+1, output), "one token past it must not")

		ctx := t.Context()
		sess, err := env.sessions.Create(ctx, "test")
		require.NoError(t, err)
		seedOversizedHistory(t, env, sess.ID, resultChars)

		sess.PromptTokens = fitLine + pastFitLine
		_, err = env.sessions.Save(ctx, sess)
		require.NoError(t, err)

		_, err = agent.summarize(ctx, compactionRequest{
			sessionID:  sess.ID,
			largeModel: model,
			trigger:    compactionTriggerGuard,
		})
		require.NoError(t, err)
		require.Len(t, stub.calls, 1)
		return toolResultTextIn(t, stub.calls[0].Prompt)
	}

	t.Run("the last context that fits keeps the history intact", func(t *testing.T) {
		text := run(t, 0)
		require.Len(t, text, resultChars, "an oversized result must reach the summarizer in full")
		require.False(t, tools.HasSpillNotice(text))
	})

	t.Run("one token past the fit line prunes", func(t *testing.T) {
		text := run(t, 1)
		require.True(t, tools.HasSpillNotice(text))
		require.Less(t, len(text), resultChars)
	})
}

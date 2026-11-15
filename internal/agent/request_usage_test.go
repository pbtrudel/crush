package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// textPrompt builds a prompt of plain user and assistant messages, with no
// provider options anywhere.
func textPrompt(texts ...string) []fantasy.Message {
	messages := make([]fantasy.Message, len(texts))
	for i, text := range texts {
		role := fantasy.MessageRoleUser
		if i%2 == 1 {
			role = fantasy.MessageRoleAssistant
		}
		messages[i] = fantasy.Message{Role: role, Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: text},
		}}
	}
	return messages
}

// TestRecordPromptFingerprintIgnoresCacheBreakpoints is the guard for the
// shared-prefix number: breakpoints move to the last two messages of every
// request by design, so a hash that included them would report a divergence
// on every single request and the number would never mean anything.
func TestRecordPromptFingerprintIgnoresCacheBreakpoints(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)

	marked := textPrompt("a", "b", "c")
	agent.applyCacheBreakpoints(marked)
	require.NotEmpty(t, marked[len(marked)-1].ProviderOptions, "the first request must carry breakpoints")

	previous, shared := agent.recordPromptFingerprint("session", marked)
	require.Zero(t, previous, "the first request of a session has nothing to compare with")
	require.Zero(t, shared)

	unmarked := textPrompt("a", "b", "c")
	previous, shared = agent.recordPromptFingerprint("session", unmarked)
	require.Equal(t, 3, previous, "the previous request carried three messages")
	require.Equal(t, 3, shared, "only the breakpoints moved, so the whole prefix is shared")
}

// TestRecordPromptFingerprintReportsTheChange pins the other direction: a
// message that really changed must shorten the shared prefix to its index.
func TestRecordPromptFingerprintReportsTheChange(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)

	agent.recordPromptFingerprint("session", textPrompt("a", "b", "c"))
	previous, shared := agent.recordPromptFingerprint("session", textPrompt("a", "B", "c"))
	require.Equal(t, 3, previous)
	require.Equal(t, 1, shared)
}

// TestRecordToolsFingerprintReportsChange covers the tools block: an extra
// tool changes it, the same list does not.
func TestRecordToolsFingerprintReportsChange(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)

	require.False(t, agent.recordToolsFingerprint("session", []fantasy.AgentTool{&recordingTool{name: "alpha"}}),
		"the first request of a session has nothing to compare with")
	require.False(t, agent.recordToolsFingerprint("session", []fantasy.AgentTool{&recordingTool{name: "alpha"}}))
	require.True(t, agent.recordToolsFingerprint("session", []fantasy.AgentTool{
		&recordingTool{name: "alpha"},
		&recordingTool{name: "beta"},
	}))
}

// finishUsage returns the usage recorded on a message's finish part.
func finishUsage(t *testing.T, msg message.Message) *message.RequestUsage {
	t.Helper()
	for _, part := range msg.Parts {
		finish, ok := part.(message.Finish)
		if !ok {
			continue
		}
		return finish.Usage
	}
	t.Fatalf("message %s has no finish part", msg.ID)
	return nil
}

// TestRunRecordsRequestUsage pins what one request cost, and how much of its
// prompt the session had already sent: the two numbers that explain a cache
// miss after the fact.
func TestRunRecordsRequestUsage(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()
	model := &stepRecordingModel{toolSteps: 1}
	agent := testSessionAgent(env, model, model, "system", &probeTool{output: "ok"})

	sess, err := env.sessions.Create(ctx, "usage")
	require.NoError(t, err)
	_, err = agent.Run(ctx, SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.NoError(t, env.messages.FlushAll(ctx))

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var assistants []message.Message
	for _, msg := range msgs {
		if msg.Role == message.Assistant {
			assistants = append(assistants, msg)
		}
	}
	require.Len(t, assistants, 2, "one message per model request")

	first, second := finishUsage(t, assistants[0]), finishUsage(t, assistants[1])
	require.NotNil(t, first, "the first request must record what it used")
	require.NotNil(t, second, "the second request must record what it used")

	require.Zero(t, first.PreviousMessages, "the first request of the turn had no earlier request to compare with")
	require.Equal(t, len(model.prompts[0]), first.PromptMessages, "the count must be what the request carried")
	require.Equal(t, len(model.prompts[1]), second.PromptMessages)
	require.Equal(t, first.PromptMessages, second.PreviousMessages)
	require.Equal(t, second.PreviousMessages, second.SharedMessages,
		"the second request only extends the first, so the whole previous prompt is shared")
	require.False(t, second.ToolsChanged, "the tool list did not change between steps")
}

// TestSummarizeRecordsRequestUsage covers the compaction request, which is a
// model request like any other and the most expensive one in a session.
func TestSummarizeRecordsRequestUsage(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 1_000, CacheReadTokens: 20_000, CacheCreationTokens: 3_000, OutputTokens: 100, TotalTokens: 24_100},
	}
	agent := newCompactionAgent(env, stub, nil, false)

	sess, err := env.sessions.Create(ctx, "summary usage")
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.NoError(t, env.messages.FlushAll(ctx))

	updated, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	summary, err := env.messages.Get(ctx, updated.SummaryMessageID)
	require.NoError(t, err)

	usage := finishUsage(t, summary)
	require.NotNil(t, usage, "a compaction request must record what it used")
	require.Len(t, stub.calls, 1)
	require.Equal(t, len(stub.calls[0].Prompt), usage.PromptMessages,
		"the count must match the messages the compaction request carried")
	require.Equal(t, int64(20_000), usage.CacheReadTokens)
	require.Equal(t, int64(3_000), usage.CacheWriteTokens)
	require.Equal(t, int64(100), usage.OutputTokens)
}

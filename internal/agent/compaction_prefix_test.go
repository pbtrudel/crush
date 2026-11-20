package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestCompaction_RequestExtendsTheLastStep is the end-to-end guard for a
// provider that caches the request prefix automatically. A live step and the
// compaction that follows it in the same session have to send the same system
// prompt and the same tools block, byte for byte, and the compaction's
// messages have to be the step's messages, then the assistant reply the turn
// stored, then one user message carrying the instruction. Anything else means
// the provider re-reads the conversation at full price.
func TestCompaction_RequestExtendsTheLastStep(t *testing.T) {
	env := testEnv(t)
	model := &toolRequestModel{text: "the answer"}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"}, &recordingTool{name: "beta"})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "first"}},
	})
	require.NoError(t, err)

	_, err = agent.Run(ctx, SessionAgentCall{Prompt: "second", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.calls, 1)
	live := model.calls[0]

	// A server connects after the step that filled the cache, which is what
	// makes the tools block the most fragile part of the prefix.
	agent.SetTools([]fantasy.AgentTool{
		&recordingTool{name: "alpha"},
		&recordingTool{name: "beta"},
		&recordingTool{name: "gamma"},
	})

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.Len(t, model.calls, 2)
	compaction := model.calls[1]

	// The system messages and the tools block are serialized ahead of the
	// conversation, so a single changed byte there drops the whole prefix.
	require.Equal(t, messagesJSON(t, live.Prompt[:1]), messagesJSON(t, compaction.Prompt[:1]),
		"the system message must be byte-identical to the last step's")
	require.Equal(t, toolsJSON(t, live.Tools), toolsJSON(t, compaction.Tools),
		"the tools block must be byte-identical to the last step's")

	// The conversation extends the step's by exactly two messages: the
	// assistant reply the live flow stored, then the instruction.
	liveMessages := messagesJSON(t, live.Prompt)
	compactionMessages := messagesJSON(t, compaction.Prompt)
	require.Len(t, compactionMessages, len(liveMessages)+2)
	require.Equal(t, liveMessages, compactionMessages[:len(liveMessages)],
		"the compaction must replay the last step's messages unchanged")
	require.Contains(t, compactionMessages[len(liveMessages)], `"role":"assistant"`)
	require.Contains(t, compactionMessages[len(liveMessages)], "the answer",
		"the stored assistant reply belongs between the history and the instruction")
	instruction := compactionMessages[len(compactionMessages)-1]
	require.Contains(t, instruction, `"role":"user"`)
	require.Contains(t, instruction, "Compact the conversation above")

	// The one allowed difference is where the explicit cache breakpoint
	// sits: the live step marked its final two messages, and appending the
	// instruction moves the last marker onto it. The step's own last
	// message must keep its marker, so the cached prefix survives; the
	// second-to-last may lose its marker to stay inside the provider's
	// breakpoint budget. Cache-control markers are stripped by
	// messagesJSON, which is why the comparison above ignores them.
	require.NotEmpty(t, compaction.Prompt[len(compaction.Prompt)-1].ProviderOptions,
		"the instruction must carry the last-message cache breakpoint")
	require.NotEmpty(t, compaction.Prompt[len(compaction.Prompt)-2].ProviderOptions,
		"the step's final message must keep a cache breakpoint")
	require.True(t, strings.HasPrefix(string(compaction.Prompt[0].Role), "system"))
}

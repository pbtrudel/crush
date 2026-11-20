package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestCompaction_RequestExtendsTheLastStep is the end-to-end guard for a
// provider that caches the request prefix automatically. A live turn and the
// compaction that follows it in the same session have to send the same system
// prompt and the same tools block, byte for byte, and the compaction's
// messages have to be the last step's messages, then the assistant reply the
// turn stored, then one user message carrying the instruction. Anything else
// means the provider re-reads the conversation at full price.
func TestCompaction_RequestExtendsTheLastStep(t *testing.T) {
	env := testEnv(t)
	model := &toolRequestModel{text: "the answer", toolSteps: 1}
	imageLimit := 1
	cfg := config.NewTestStore(&config.Config{Options: &config.Options{MaxImages: &imageLimit}})
	agent := newVisionCompactionAgent(env, model, cfg,
		&recordingTool{name: "alpha"}, &recordingTool{name: "beta"}, &probeTool{output: "tool output"})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	// Two images, so the one-image budget has to drop the oldest in both
	// requests.
	for _, name := range []string{"first.png", "second.png"} {
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "look at " + name},
				message.BinaryContent{Path: name, MIMEType: "image/png", Data: []byte("fake-png")},
			},
		})
		require.NoError(t, err)
	}

	// The turn calls the probe tool and answers on its second step, so the
	// last step's request already carries a tool result.
	_, err = agent.Run(ctx, SessionAgentCall{Prompt: "second", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.calls, 2)
	live := model.calls[len(model.calls)-1]

	// A server connects after the last step, which is what makes the tools
	// block the most fragile part of the prefix.
	agent.SetTools([]fantasy.AgentTool{
		&recordingTool{name: "alpha"},
		&recordingTool{name: "beta"},
		&probeTool{output: "tool output"},
		&recordingTool{name: "gamma"},
	})

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.Len(t, model.calls, 3)
	compaction := model.calls[2]

	// The system messages and the tools block are serialized ahead of the
	// conversation, so a single changed byte there drops the whole prefix.
	require.Equal(t, messagesJSON(t, live.Prompt[:1]), messagesJSON(t, compaction.Prompt[:1]),
		"the system message must be byte-identical to the last step's")
	require.Equal(t, toolsJSON(t, live.Tools), toolsJSON(t, compaction.Tools),
		"the tools block must be byte-identical to the last step's")

	// The cache breakpoint sits on the same tool the live step marked, and
	// on that tool only.
	require.Len(t, live.Tools, 3)
	for i := range live.Tools {
		require.Equal(t, toolBreakpointJSON(t, live.Tools[i]), toolBreakpointJSON(t, compaction.Tools[i]),
			"tool %d must carry the cache markers the live step sent", i)
		if i == len(compaction.Tools)-1 {
			require.NotEqual(t, "{}", toolBreakpointJSON(t, compaction.Tools[i]),
				"the last tool must carry the cache breakpoint")
			continue
		}
		require.Equal(t, "{}", toolBreakpointJSON(t, compaction.Tools[i]),
			"only the last tool may carry the cache breakpoint")
	}

	// The conversation extends the step's by exactly two messages: the
	// assistant reply the live flow stored, then the instruction.
	liveMessages := messagesJSON(t, live.Prompt)
	compactionMessages := messagesJSON(t, compaction.Prompt)
	require.Len(t, compactionMessages, len(liveMessages)+2)
	require.Equal(t, liveMessages, compactionMessages[:len(liveMessages)],
		"the compaction must replay the last step's messages unchanged")
	liveText := strings.Join(liveMessages, "\n")
	require.Contains(t, liveText, imageOmittedText,
		"the live step must omit the oldest image, so the comparison covers the image budget")
	require.Contains(t, liveText, "tool output",
		"the last step's request must carry the tool result")
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

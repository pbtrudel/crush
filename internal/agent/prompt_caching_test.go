package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingTool is a tool that remembers the provider options set on it, so
// tests can assert on cache breakpoint placement.
type recordingTool struct {
	name string
	opts fantasy.ProviderOptions
}

func (t *recordingTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: t.name} }

func (t *recordingTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.NewTextResponse(""), nil
}

func (t *recordingTool) ProviderOptions() fantasy.ProviderOptions { return t.opts }

func (t *recordingTool) SetProviderOptions(opts fantasy.ProviderOptions) { t.opts = opts }

func toolNames(tools []fantasy.AgentTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Info().Name)
	}
	return names
}

// TestPreparePrompt_ReplayMatchesLiveUserMessage is the guard for the prompt
// cache: the user message a turn sends live (prompt text plus file parts) and
// the same message as replayed on the next turn must be byte-identical, or
// every turn misses the provider's prefix cache.
func TestPreparePrompt_ReplayMatchesLiveUserMessage(t *testing.T) {
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	// Surrounding whitespace and both attachment kinds is exactly the shape
	// that used to replay differently from the live request.
	call := SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "  look at this\n",
		Attachments: []message.Attachment{
			{FileName: "notes.txt", FilePath: "/tmp/notes.txt", MimeType: "text/plain", Content: []byte("notes")},
			{FileName: "photo.png", FilePath: "/tmp/photo.png", MimeType: "image/png", Content: []byte("fake-png")},
		},
	}
	_, err = agent.createUserMessage(ctx, call)
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	// Live form: the prompt string and the files passed to the provider.
	livePrompt := message.PromptWithTextAttachments(call.Prompt, call.Attachments)
	_, liveFiles := agent.preparePrompt(msgs, true, call.Attachments...)
	require.Len(t, liveFiles, 1, "only the image is sent as a file part")

	// Replay form: what the next turn rebuilds from the session store.
	history, replayFiles := agent.preparePrompt(msgs, true)
	require.Empty(t, replayFiles)
	require.Len(t, history, 2)
	text, ok := fantasy.AsMessagePart[fantasy.TextPart](history[1].Content[0])
	require.True(t, ok)
	require.Equal(t, livePrompt, text.Text,
		"replayed user text must match the live prompt byte for byte")
	file, ok := fantasy.AsMessagePart[fantasy.FilePart](history[1].Content[1])
	require.True(t, ok)
	require.Equal(t, liveFiles[0].Filename, file.Filename,
		"replayed attachment name must match the live file part")
}

func TestApplyCacheBreakpoints(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	messages := []fantasy.Message{
		fantasy.NewSystemMessage("system"),
		fantasy.NewUserMessage("one"),
		fantasy.NewUserMessage("two"),
		fantasy.NewUserMessage("three"),
	}
	agent.applyCacheBreakpoints(messages)

	require.NotEmpty(t, messages[0].ProviderOptions, "system message must carry a breakpoint")
	require.Empty(t, messages[1].ProviderOptions, "interior messages must not carry breakpoints")
	require.NotEmpty(t, messages[2].ProviderOptions, "second-to-last message must carry a breakpoint")
	require.NotEmpty(t, messages[3].ProviderOptions, "last message must carry a breakpoint")
}

func TestApplyCacheBreakpoints_SystemOnly(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	messages := []fantasy.Message{fantasy.NewSystemMessage("system")}
	agent.applyCacheBreakpoints(messages)
	require.NotEmpty(t, messages[0].ProviderOptions)
}

func TestApplyToolCacheBreakpoint(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	stale := &recordingTool{name: "a", opts: fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}}
	last := &recordingTool{name: "b"}
	agent.applyToolCacheBreakpoint([]fantasy.AgentTool{stale, last})

	require.Empty(t, stale.opts, "the breakpoint must move off the previous last tool")
	require.NotEmpty(t, last.opts, "the last tool must carry the breakpoint")
}

// TestToolsForStep_AdoptsAdditionsAndIgnoresRemovals pins the mid-turn tool
// list policy: tools registered after the turn started (a lazy MCP server the
// model just activated) join the palette, while tools that vanish from the
// registry do not, because a shrinking palette rewrites the tools block and
// invalidates the prompt cache for no benefit.
func TestToolsForStep_AdoptsAdditionsAndIgnoresRemovals(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	agent.SetTools([]fantasy.AgentTool{
		&recordingTool{name: "a"},
		&recordingTool{name: "b"},
		&recordingTool{name: "c"},
	})
	turnTools := []fantasy.AgentTool{&recordingTool{name: "a"}, &recordingTool{name: "b"}}

	adopted, changed := agent.toolsForStep(t.Context(), "", turnTools)
	require.True(t, changed)
	require.Equal(t, []string{"a", "b", "c"}, toolNames(adopted),
		"a tool registered after the turn began must be adopted in sorted order")

	// The registry loses "c": the turn's palette must keep it.
	agent.SetTools([]fantasy.AgentTool{&recordingTool{name: "a"}})
	kept, changed := agent.toolsForStep(t.Context(), "", adopted)
	require.False(t, changed)
	require.Equal(t, []string{"a", "b", "c"}, toolNames(kept),
		"a tool that disappears from the registry must not be dropped mid-turn")
}

// TestNormalizePromptMessages checks that in-flight messages are rewritten
// into the same form the persistence path stores, which is what keeps the
// live request and the next turn's replay identical.
func TestNormalizePromptMessages(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	messages := []fantasy.Message{
		{
			Role: fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{
				fantasy.ToolCallPart{ToolCallID: "1", ToolName: "view", Input: "not json"},
			},
		},
		{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "1",
					Output: fantasy.ToolResultOutputContentMedia{
						Data:      "aGk=",
						MediaType: "image/png",
					},
				},
			},
		},
	}
	agent.normalizePromptMessages(messages)

	toolCall, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](messages[0].Content[0])
	require.True(t, ok)
	require.Equal(t, "{}", toolCall.Input, "malformed tool input must be repaired like the persisted form")

	result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](messages[1].Content[0])
	require.True(t, ok)
	media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output)
	require.True(t, ok)
	require.Equal(t, "Loaded image/png content", media.Text,
		"media without a caption must gain the same default the store writes")

	// Idempotent: the second pass must not change anything.
	agent.normalizePromptMessages(messages)
	toolCall, _ = fantasy.AsMessagePart[fantasy.ToolCallPart](messages[0].Content[0])
	assert.Equal(t, "{}", toolCall.Input)
}

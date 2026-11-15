package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// fixedResponseTool answers with one prepared response, so a test can drive
// the wrapper without a real tool.
type fixedResponseTool struct {
	response fantasy.ToolResponse
}

func (t *fixedResponseTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: "fixed"} }

func (t *fixedResponseTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return t.response, nil
}

func (t *fixedResponseTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (t *fixedResponseTool) SetProviderOptions(fantasy.ProviderOptions) {}

// TestBoundedOutputTool pins the contract of the wrapper: it bounds text and
// error content where the tool returns it, leaves media alone, and otherwise
// stays transparent to the agent.
func TestBoundedOutputTool(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "prompt").(*sessionAgent)
	bound := func(tool fantasy.AgentTool) fantasy.AgentTool {
		return boundedOutputTool{inner: tool, bound: agent.truncateToolOutput}
	}

	t.Run("oversized text is bounded", func(t *testing.T) {
		t.Parallel()
		// A 300,000 character output is the sort a single verbose command
		// produces, and it is what the free-text providers charge for.
		inner := bound(&fixedResponseTool{response: fantasy.NewTextResponse(strings.Repeat("a", 300_000))})
		resp, err := inner.Run(t.Context(), fantasy.ToolCall{})
		require.NoError(t, err)
		require.False(t, resp.IsError)
		require.LessOrEqual(t, len(resp.Content), 50_000)
		require.True(t, tools.HasSpillNotice(resp.Content))
	})

	t.Run("short text is unchanged", func(t *testing.T) {
		t.Parallel()
		inner := bound(&fixedResponseTool{response: fantasy.NewTextResponse("short")})
		resp, err := inner.Run(t.Context(), fantasy.ToolCall{})
		require.NoError(t, err)
		require.Equal(t, "short", resp.Content)
	})

	t.Run("error replies are bounded too", func(t *testing.T) {
		t.Parallel()
		inner := bound(&fixedResponseTool{response: fantasy.NewTextErrorResponse(strings.Repeat("e", 300_000))})
		resp, err := inner.Run(t.Context(), fantasy.ToolCall{})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.LessOrEqual(t, len(resp.Content), 50_000)
		require.True(t, tools.HasSpillNotice(resp.Content))
	})

	t.Run("media keeps its caption", func(t *testing.T) {
		t.Parallel()
		media := fantasy.ToolResponse{Type: "image", Content: strings.Repeat("c", 300_000), Data: []byte("png")}
		inner := bound(&fixedResponseTool{response: media})
		resp, err := inner.Run(t.Context(), fantasy.ToolCall{})
		require.NoError(t, err)
		require.Equal(t, media.Content, resp.Content, "a media caption is budgeted by the image budget, not here")
		require.Equal(t, media.Data, resp.Data)
	})

	t.Run("the wrapper is transparent to the agent", func(t *testing.T) {
		t.Parallel()
		recorder := &recordingTool{name: "alpha"}
		inner := bound(recorder)
		require.Equal(t, "alpha", inner.Info().Name)
		opts := fantasy.ProviderOptions{
			anthropic.Name: &anthropic.ProviderCacheControlOptions{
				CacheControl: anthropic.CacheControl{Type: "ephemeral"},
			},
		}
		inner.SetProviderOptions(opts)
		require.Equal(t, opts, inner.ProviderOptions())
		require.Equal(t, opts, recorder.ProviderOptions(), "the options must land on the real tool")
	})
}

// toolResultText returns the text of the first text tool result part in
// messages.
func toolResultText(t *testing.T, messages []fantasy.Message) string {
	t.Helper()
	for _, msg := range messages {
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
	t.Fatal("no tool result text in the prompt")
	return ""
}

// TestOversizedToolOutputIsBoundedInTheRunningTurn is the reason the tool is
// bounded at the source: fantasy carries a tool's own response into every
// later request of the turn, so an unbounded output is sent again on each
// step, and the next turn replays the stored (bounded) text instead, which
// makes everything after it a prompt cache miss.
func TestOversizedToolOutputIsBoundedInTheRunningTurn(t *testing.T) {
	env := testEnv(t)
	model := &stepRecordingModel{toolSteps: 1}
	probe := &probeTool{output: strings.Repeat("x", 300_000)}
	agent := testSessionAgent(env, model, model, "system", probe)

	sess, err := env.sessions.Create(t.Context(), "oversized")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.prompts, 2, "the turn must have run one tool step and then finished")

	live := toolResultText(t, model.prompts[1])
	require.LessOrEqual(t, len(live), 50_000, "the second request must not carry the full output")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var stored string
	for _, msg := range msgs {
		if msg.Role != message.Tool {
			continue
		}
		stored = msg.ToolResults()[0].Content
	}
	require.NotEmpty(t, stored)
	require.Equal(t, stored, live, "the text sent in the turn must be the text stored for replay")
}

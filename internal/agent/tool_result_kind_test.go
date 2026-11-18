package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStampLibraryErrorKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result message.ToolResult
		want   string
	}{
		{
			name:   "unknown tool",
			result: message.ToolResult{Content: "tool not found: nope. Available tools: view", IsError: true},
			want:   `{"error_kind":"unknown_tool"}`,
		},
		{
			name:   "keeps hook metadata",
			result: message.ToolResult{Content: "invalid parameters: bad", IsError: true, Metadata: `{"hook":{"hook_count":1}}`},
			want:   `{"hook":{"hook_count":1},"error_kind":"invalid_arguments"}`,
		},
		{
			name:   "a kind already set wins",
			result: message.ToolResult{Content: "invalid parameters: bad", IsError: true, Metadata: `{"error_kind":"permission_denied"}`},
			want:   `{"error_kind":"permission_denied"}`,
		},
		{
			name:   "not an error",
			result: message.ToolResult{Content: "tool not found: in this output"},
			want:   "",
		},
		{
			name:   "an error outside the list",
			result: message.ToolResult{Content: "Failed to write file: disk full", IsError: true},
			want:   "",
		},
		{
			name:   "the prefix must start the text",
			result: message.ToolResult{Content: "MCP said: tool not found: x", IsError: true},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stampLibraryErrorKind(tt.result)
			require.Equal(t, tt.want, got.Metadata)
			require.Equal(t, tt.result.Content, got.Content, "the text the model reads is unchanged")
			require.Equal(t, tt.result.IsError, got.IsError)
		})
	}
}

// kindProbeParams has one required parameter, so the library checks for it
// before the call runs.
type kindProbeParams struct {
	Path string `json:"path" description:"A path"`
}

// TestLibraryErrorKindsFromThePinnedLibrary drives one step whose calls
// make charm.land/fantasy refuse in each way it can, and reads the kinds
// back from the session log. A reworded library message fails here.
func TestLibraryErrorKindsFromThePinnedLibrary(t *testing.T) {
	env := testEnv(t)
	typed := fantasy.NewAgentTool("typed", "Takes a path.", func(_ context.Context, p kindProbeParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ran with " + p.Path), nil
	})
	call := func(id, name, input string) fantasy.StreamPart {
		return fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: id, ToolCallName: name, ToolCallInput: input}
	}
	model := &scriptedModel{script: []scriptedCall{
		{parts: []fantasy.StreamPart{
			call("unknown", "nope", `{}`),
			call("not-an-object", "typed", `[1]`),
			call("missing", "typed", `{}`),
			call("wrong-type", "typed", `{"path":5}`),
			call("not-json", "typed", ``),
			call("fine", "typed", `{"path":"a"}`),
			{
				Type:         fantasy.StreamPartTypeFinish,
				FinishReason: fantasy.FinishReasonToolCalls,
				Usage:        fantasy.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010},
			},
		}},
		answer("done", fantasy.FinishReasonStop, 1000),
	}}
	agent := testSessionAgent(env, model, model, "system", typed)
	sess, err := env.sessions.Create(t.Context(), "library kinds")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	results := map[string]message.ToolResult{}
	for _, m := range msgs {
		for _, tr := range m.ToolResults() {
			results[tr.ToolCallID] = tr
		}
	}

	tests := []struct {
		id     string
		prefix string
		want   tools.ErrorKind
	}{
		{id: "unknown", prefix: "tool not found: nope", want: tools.ErrorKindUnknownTool},
		{id: "not-an-object", prefix: "invalid JSON input: ", want: tools.ErrorKindInvalidArguments},
		{id: "missing", prefix: "missing required parameter: path", want: tools.ErrorKindInvalidArguments},
		{id: "wrong-type", prefix: "invalid parameters: ", want: tools.ErrorKindInvalidArguments},
		{id: "not-json", prefix: sanitizedToolCallResult, want: tools.ErrorKindInvalidArguments},
	}
	for _, tt := range tests {
		result, ok := results[tt.id]
		require.True(t, ok, "call %s has a stored result", tt.id)
		require.True(t, result.IsError, "call %s", tt.id)
		require.True(t, strings.HasPrefix(result.Content, tt.prefix), "call %s: the library wrote %q", tt.id, result.Content)
		require.Equal(t, string(tt.want), gjson.Get(result.Metadata, "error_kind").String(), "call %s", tt.id)
	}

	fine := results["fine"]
	require.False(t, fine.IsError)
	require.Equal(t, "ran with a", fine.Content)
	require.False(t, gjson.Get(fine.Metadata, "error_kind").Exists())
}

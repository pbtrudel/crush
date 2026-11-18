package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHookedToolBlockKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cmd      string
		content  string
		stopTurn bool
	}{
		{name: "deny", cmd: `echo "blocked" >&2; exit 2`, content: "Tool call blocked by hook. Reason: blocked"},
		{name: "halt", cmd: `echo '{"halt":true,"reason":"turn over"}'`, content: "Turn halted by hook. Reason: turn over", stopTurn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inner := &fakeTool{name: "bash"}
			tool := newHookedTool(inner, newRunner(t, tt.cmd))

			resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
			require.NoError(t, err)
			require.False(t, inner.called)
			require.True(t, resp.IsError)
			require.Equal(t, tt.stopTurn, resp.StopTurn)
			require.Equal(t, tt.content, resp.Content, "the text the model reads is unchanged")
			require.Equal(t, string(tools.ErrorKindHookDenied), gjson.Get(resp.Metadata, "error_kind").String())
			require.True(t, gjson.Get(resp.Metadata, "hook").Exists(), "the hook metadata is kept: %s", resp.Metadata)
		})
	}
}

// A hook that lets a call through leaves the tool's own kind alone, and
// adds none to a call that succeeded.
func TestHookedToolPassThroughKind(t *testing.T) {
	t.Parallel()

	denied := &fakeTool{name: "bash", resp: tools.NewPermissionDeniedResponse()}
	resp, err := newHookedTool(denied, newRunner(t, `exit 0`)).Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
	require.NoError(t, err)
	require.Equal(t, string(tools.ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())

	ok := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	resp, err = newHookedTool(ok, newRunner(t, `exit 0`)).Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
	require.NoError(t, err)
	require.False(t, gjson.Get(resp.Metadata, "error_kind").Exists())
}

func TestSettledToolCallsAreInterrupted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		runErr error
		want   string
	}{
		{name: "canceled", runErr: context.Canceled, want: canceledToolCallResult},
		{name: "abandoned", want: abandonedToolCallResult},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := testEnv(t)
			agent := &sessionAgent{messages: env.messages}
			sess, err := env.sessions.Create(t.Context(), "t")
			require.NoError(t, err)
			assistant, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "done", Name: "view", Input: `{}`, Finished: true},
					message.ToolCall{ID: "open", Name: "bash", Input: `{}`, Finished: true},
				},
			})
			require.NoError(t, err)

			require.NoError(t, agent.settleTurn(t.Context(), &assistant, answeredOnly("done"), tt.runErr))

			msgs, err := env.messages.List(t.Context(), sess.ID)
			require.NoError(t, err)
			var results []message.ToolResult
			for _, m := range msgs {
				results = append(results, m.ToolResults()...)
			}
			require.Len(t, results, 1)
			require.Equal(t, "open", results[0].ToolCallID)
			require.True(t, results[0].IsError)
			require.Equal(t, tt.want, results[0].Content, "the text the model reads is unchanged")
			require.Equal(t, `{"error_kind":"interrupted"}`, results[0].Metadata)
		})
	}
}

// A call a dead process left without a reply is settled by the repair,
// which goes through the same writer.
func TestRepairedToolCallsAreInterrupted(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "t")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "orphan", Name: "bash", Input: `{}`, Finished: true}},
	})
	require.NoError(t, err)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	repaired, err := repairInterruptedToolCalls(t.Context(), env.messages, msgs)
	require.NoError(t, err)
	var results []message.ToolResult
	for _, m := range repaired {
		results = append(results, m.ToolResults()...)
	}
	require.Len(t, results, 1)
	require.Equal(t, interruptedToolCallResult, results[0].Content)
	require.Equal(t, string(tools.ErrorKindInterrupted), gjson.Get(results[0].Metadata, "error_kind").String())
}

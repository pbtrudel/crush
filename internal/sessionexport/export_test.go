package sessionexport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestExportMarkdownIncludesConversation(t *testing.T) {
	t.Parallel()

	sess := session.Session{
		ID:        "session-1",
		Title:     "Fix the bug",
		CreatedAt: 1_700_000_000,
		UpdatedAt: 1_700_000_100,
	}
	msgs := []message.Message{
		{
			ID:   "m1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
			},
		},
		{
			ID:   "m2",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "thinking hard"},
				message.TextContent{Text: "hi there"},
				message.ToolCall{ID: "t1", Name: "bash", Input: `{"command":"ls"}`},
			},
		},
		{
			ID:   "m3",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "t1", Name: "bash", Content: "file.txt"},
			},
		},
	}

	out := Markdown(sess, msgs)

	require.Contains(t, out, "# Fix the bug")
	require.Contains(t, out, "session-1")
	require.Contains(t, out, "## User")
	require.Contains(t, out, "hello")
	require.Contains(t, out, "## Assistant")
	require.Contains(t, out, "thinking hard")
	require.Contains(t, out, "hi there")
	require.Contains(t, out, "bash")
	require.Contains(t, out, `"command": "ls"`)
	require.Contains(t, out, "file.txt")
}

func TestExportMarkdownIncludesShellCommandsAndSkipsHiddenText(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			ID:   "m1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "visible"},
				message.TextContent{Text: "hidden", Hidden: true},
				message.ShellCommand{Command: "echo hi", Output: "hi", ExitCode: 0},
			},
		},
	}

	out := Markdown(session.Session{Title: "Shell"}, msgs)

	require.Contains(t, out, "visible")
	require.NotContains(t, out, "hidden")
	require.Contains(t, out, "echo hi")
	require.Contains(t, out, "hi")
}

package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestHumanDuration(t *testing.T) {
	t.Parallel()
	require.Equal(t, "5s", humanDuration(5*time.Second))
	require.Equal(t, "1m 05s", humanDuration(65*time.Second))
	require.Equal(t, "2h 03m", humanDuration(2*time.Hour+3*time.Minute))
	require.Equal(t, "0s", humanDuration(-time.Second))
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()
	require.Equal(t, "987", formatTokens(987))
	require.Equal(t, "1.2k", formatTokens(1234))
	require.Equal(t, "1.5m", formatTokens(1_500_000))
}

func TestFenceGrowsPastInnerFences(t *testing.T) {
	t.Parallel()
	require.Equal(t, "```\nplain\n```", fence("plain"))

	nested := fence("```\ninner\n```")
	require.True(t, strings.HasPrefix(nested, "````\n"), "outer fence must grow: %q", nested)
	require.True(t, strings.HasSuffix(nested, "\n````"))
}

func TestTrimLines(t *testing.T) {
	t.Parallel()
	body, hidden := trimLines("a\nb\nc", 5)
	require.Equal(t, "a\nb\nc", body)
	require.Zero(t, hidden)

	body, hidden = trimLines("a\nb\nc\nd", 2)
	require.Equal(t, "a\nb", body)
	require.Equal(t, 2, hidden)
}

func TestTailText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "short", tailText("short", 100))

	long := "line1\nline2\nline3\nline4"
	tail := tailText(long, 12)
	require.True(t, strings.HasPrefix(tail, "…\n"), "tail must be marked: %q", tail)
	require.True(t, strings.HasSuffix(tail, "line4"))
	require.NotContains(t, tail, "line1\n")
}

func TestToolMainParam(t *testing.T) {
	t.Parallel()
	require.Equal(t, "npm test", toolMainParam(`{"command":"npm test"}`))
	require.Equal(t, "x", toolMainParam(`{"file_path":"src/a.go","command":"x"}`), "command outranks file_path")
	require.Equal(t, "fallback", toolMainParam(`{"something":"fallback"}`))
	require.Equal(t, "", toolMainParam(`not json`))
	require.Equal(t, "", toolMainParam(`{}`))
	require.Equal(t, "caps at", toolMainParam(`{"query":"caps at"}`))
}

func TestRenderToolCallLine(t *testing.T) {
	t.Parallel()
	req := require.New(t)
	line := renderToolCallLine(message.ToolCall{Name: "bash", Input: `{"command":"ls -la"}`})
	require.Contains(t, line, "🔧")
	require.Contains(t, line, "`bash`")
	require.Contains(t, line, "ls -la")
	req.NotEmpty(renderToolCallLine(message.ToolCall{Name: "weird"}))
	req.Contains(renderToolCallLine(message.ToolCall{}), "tool")
}

func TestRenderToolResultMessage(t *testing.T) {
	t.Parallel()
	call := message.ToolCall{Name: "bash", Input: `{"command":"npm test"}`}

	out := renderToolResultMessage(call, message.ToolResult{Name: "bash", Content: "ok"})
	require.Contains(t, out, "npm test")
	require.Contains(t, out, "```\nok\n```")

	errOut := renderToolResultMessage(call, message.ToolResult{Name: "bash", Content: "boom", IsError: true})
	require.Contains(t, errOut, "failed")

	empty := renderToolResultMessage(message.ToolCall{Name: "view"}, message.ToolResult{Name: "view"})
	require.Contains(t, empty, "`view`")
	require.NotContains(t, empty, "```")

	long := strings.Repeat("x\n", 40)
	trimmed := renderToolResultMessage(call, message.ToolResult{Name: "bash", Content: long})
	require.Contains(t, trimmed, "lines hidden")
}

func TestRenderReasoningLine(t *testing.T) {
	t.Parallel()
	require.Empty(t, renderReasoningLine(message.ReasoningContent{Thinking: "..."}), "unfinished thinking is not mirrored")
	line := renderReasoningLine(message.ReasoningContent{Thinking: "x", StartedAt: 100, FinishedAt: 112})
	require.Contains(t, line, "thought for 12s")
}

func TestRenderAssistantTimeline(t *testing.T) {
	t.Parallel()
	msg := message.Message{Parts: []message.ContentPart{
		message.ReasoningContent{StartedAt: 10, FinishedAt: 20},
		message.TextContent{Text: "**hello**"},
		message.TextContent{Text: "hidden", Hidden: true},
		message.ToolCall{Name: "bash", Input: `{"command":"go test ./..."}`},
	}}

	out := renderAssistantTimeline(msg)
	require.Contains(t, out, "thought for 10s")
	require.Contains(t, out, "**hello**")
	require.NotContains(t, out, "hidden")
	require.Contains(t, out, "go test ./...")
}

func TestSplitMessage(t *testing.T) {
	t.Parallel()
	require.Nil(t, splitMessage(""))
	require.Nil(t, splitMessage("   \n  "))

	short := "hello"
	require.Equal(t, []string{short}, splitMessage(short))

	var sb strings.Builder
	for range 500 {
		sb.WriteString("0123456789\n")
	}
	chunks := splitMessage(sb.String())
	require.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		require.LessOrEqual(t, len(c), maxMessageLen)
	}
	require.Equal(t, sb.String(), strings.Join(chunks, "\n"), "chunks must rejoin losslessly")

	// A single line longer than the safe limit is cut hard.
	long := strings.Repeat("x", maxMessageLen+50)
	chunks = splitMessage(long)
	require.Len(t, chunks, 2)
	require.Equal(t, long, strings.Join(chunks, ""))
}

func TestSpinnerFrame(t *testing.T) {
	t.Parallel()
	require.Equal(t, spinnerFrames[0], spinnerFrame(0))
	require.Equal(t, spinnerFrames[1], spinnerFrame(1))
	require.Equal(t, spinnerFrames[0], spinnerFrame(len(spinnerFrames)))
}

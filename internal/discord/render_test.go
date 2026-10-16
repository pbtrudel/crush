package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
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

func TestToolVerb(t *testing.T) {
	t.Parallel()
	require.Equal(t, "Editing main.go", toolVerb("edit", "main.go"))
	require.Equal(t, "Editing", toolVerb("multiedit", ""))
	require.Equal(t, "Reading src/a.go", toolVerb("view", "src/a.go"))
	require.Equal(t, "Searching footer", toolVerb("grep", "footer"))
	require.Equal(t, "Running go test ./...", toolVerb("bash", "go test ./..."))
	require.Equal(t, "Running", toolVerb("bash", ""))
	require.Equal(t, "Writing notes.md", toolVerb("write", "notes.md"))
	require.Equal(t, "Delegating", toolVerb("agent", "do the thing"))
	require.Equal(t, "Running weird", toolVerb("weird", ""))
	require.Equal(t, "Running weird — x", toolVerb("weird", "x"))
	require.Equal(t, "Running tool", toolVerb("", ""))
}

func TestRenderToolSummary(t *testing.T) {
	t.Parallel()
	require.Empty(t, renderToolSummary(nil, 0))

	out := renderToolSummary(map[string]int{"bash": 3, "read": 1, "edit": 2}, 1)
	require.Equal(t, "-# 🔧 bash ×3 · edit ×2 · read ×1 · (1 failed)", out)

	only := renderToolSummary(map[string]int{"bash": 1}, 0)
	require.Equal(t, "-# 🔧 bash ×1", only)

	failuresOnly := renderToolSummary(nil, 2)
	require.Equal(t, "-# 🔧 (2 failed)", failuresOnly)
}

func TestRenderMetricsLine(t *testing.T) {
	t.Parallel()
	sess := session.Session{
		PromptTokens:         12600,
		CompletionTokens:     200,
		TotalContextTokens:   400,
		TotalCacheReadTokens: 100,
	}
	require.Equal(t, "-# 📊 context 10% · cache 25%", renderMetricsLine(sess, 128000))

	sess.EstimatedUsage = true
	require.Equal(t, "-# 📊 context ~10% · cache 25%", renderMetricsLine(sess, 128000))
	sess.EstimatedUsage = false

	// Without a resolvable context window only the cache share renders.
	require.Equal(t, "-# 📊 cache 25%", renderMetricsLine(sess, 0))

	// A fresh session shows a zero percentage while the window is known.
	require.Equal(t, "-# 📊 context 0%", renderMetricsLine(session.Session{}, 128000))
	require.Empty(t, renderMetricsLine(session.Session{}, 0))
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
	require.NotContains(t, out, "go test ./...", "tool calls are summarized in the footer, not the transcript")
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

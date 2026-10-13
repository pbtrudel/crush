package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// Status icons used across the live status line and one-off messages.
const (
	iconThinking   = "🤔"
	iconWriting    = "✍️"
	iconTool       = "🔧"
	iconDone       = "✅"
	iconError      = "❌"
	iconCancelled  = "⏹️"
	iconPermission = "🛑"
	iconQuestion   = "❓"
	iconLocal      = "💻"
	iconBot        = "🤖"
	iconBrain      = "🧠"
)

// Reaction emojis used for permission approvals.
const (
	reactionAllow        = "✅"
	reactionAllowSession = "🔁"
	reactionDeny         = "❌"
)

// spinnerFrames animates the live status line; one frame is consumed per
// flush (the edit throttle paces it).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// toolResultMaxLines caps how much tool output is mirrored to Discord;
// the TUI collapses at ten lines and the rest is summarized as hidden.
const toolResultMaxLines = 15

// spinnerFrame returns the animation frame for a flush counter.
func spinnerFrame(i int) string {
	if len(spinnerFrames) == 0 {
		return ""
	}
	return spinnerFrames[i%len(spinnerFrames)]
}

// humanDuration formats a duration for Discord ("12s", "1m 5s", "2h 3m").
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// formatTokens renders a token count compactly ("987", "12.3k", "1.2m").
func formatTokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	}
}

// fence renders content as a Discord code block. The fence grows past
// any backtick run inside the content so nested fences cannot break the
// block (CommonMark fence rules, supported by Discord).
func fence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	ticks := strings.Repeat("`", max(3, longest+1))
	return ticks + "\n" + strings.TrimRight(content, "\n") + "\n" + ticks
}

// trimLines keeps at most max lines of content, returning the trimmed
// body and the number of hidden lines.
func trimLines(content string, maxLines int) (string, int) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n"), 0
	}
	return strings.Join(lines[:maxLines], "\n"), len(lines) - maxLines
}

// tailText returns the last part of s that fits in max bytes, cut on a
// line boundary and prefixed with an ellipsis marker when truncated.
func tailText(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := len(s) - maxBytes
	if idx := strings.IndexByte(s[cut:], '\n'); idx >= 0 {
		cut += idx + 1
	}
	return "…\n" + s[cut:]
}

// toolMainParam extracts a short, human-readable summary of a tool
// call's input: the first populated value from a preference list of
// common parameter names.
func toolMainParam(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}
	preferred := []string{
		"command", "cmd", "file_path", "path", "pattern", "query",
		"url", "prompt", "description", "name", "resource", "uri",
	}
	for _, key := range preferred {
		if v, ok := fields[key].(string); ok && v != "" {
			return compactLine(v, 100)
		}
	}
	for _, v := range fields {
		if s, ok := v.(string); ok && s != "" {
			return compactLine(s, 100)
		}
	}
	return ""
}

// compactLine flattens whitespace and caps a string at n runes.
func compactLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, n)
}

// renderToolCallLine renders one tool call as a single timeline line,
// mirroring the TUI's "● ToolName param" header.
func renderToolCallLine(tc message.ToolCall) string {
	if tc.Name == "" {
		return iconTool + " tool"
	}
	line := fmt.Sprintf("%s `%s`", iconTool, tc.Name)
	if param := toolMainParam(tc.Input); param != "" {
		line += " — " + param
	}
	return line
}

// renderToolResultMessage renders the standalone thread message posted
// when a tool completes: the call line plus a trimmed, fenced body.
func renderToolResultMessage(call message.ToolCall, tr message.ToolResult) string {
	name := tr.Name
	if name == "" {
		name = call.Name
	}
	if name == "" {
		name = "tool"
	}
	head := fmt.Sprintf("%s `%s`", iconTool, name)
	if param := toolMainParam(call.Input); param != "" {
		head += " — " + param
	}
	if tr.IsError {
		head = fmt.Sprintf("%s `%s` failed", iconError, name)
		if param := toolMainParam(call.Input); param != "" {
			head += " — " + param
		}
	}

	body, hidden := trimLines(tr.Content, toolResultMaxLines)
	if strings.TrimSpace(body) == "" {
		if hidden > 0 {
			return fmt.Sprintf("%s\n-# %d lines hidden", head, hidden)
		}
		return head
	}
	out := head + "\n" + fence(body)
	if hidden > 0 {
		out += fmt.Sprintf("\n-# %d lines hidden", hidden)
	}
	return out
}

// renderReasoningLine renders a finished thinking block as one line.
// While thinking is still streaming the status header covers it, so
// partial thoughts are never mirrored.
func renderReasoningLine(rc message.ReasoningContent) string {
	if rc.StartedAt <= 0 || rc.FinishedAt <= rc.StartedAt {
		return ""
	}
	secs := rc.FinishedAt - rc.StartedAt
	return fmt.Sprintf("%s *thought for %s*", iconBrain, humanDuration(time.Duration(secs)*time.Second))
}

// renderAssistantTimeline renders the parts of an in-flight assistant
// message as a Discord timeline: thinking markers, streamed markdown
// text, and one line per tool call. Tool results are separate messages
// and are not part of this rendering.
func renderAssistantTimeline(msg message.Message) string {
	var lines []string
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.ReasoningContent:
			if line := renderReasoningLine(p); line != "" {
				lines = append(lines, line)
			}
		case message.TextContent:
			if p.Hidden || strings.TrimSpace(p.Text) == "" {
				continue
			}
			lines = append(lines, strings.TrimSpace(p.Text))
		case message.ToolCall:
			lines = append(lines, renderToolCallLine(p))
		case message.Finish:
			// Rendered by the status header instead.
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

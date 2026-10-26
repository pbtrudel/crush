package discord

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
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
	iconMetrics    = "📊"
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
	return truncateRunes(plainLine(s), n)
}

// plainLine flattens whitespace without dropping any words, so a
// multi-line description still fits on one Discord line.
func plainLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// toolVerb renders the friendly state label for a tool run, appending
// the call's main parameter when one is known ("Editing main.go").
func toolVerb(name, param string) string {
	verb := ""
	switch name {
	case "edit", "multiedit":
		verb = "Editing"
	case "view", "read":
		verb = "Reading"
	case "grep", "glob", "ls", "sourcegraph", "web_search":
		verb = "Searching"
	case "write":
		verb = "Writing"
	case "bash":
		verb = "Running"
	case "fetch", "agentic_fetch", "web_fetch":
		verb = "Fetching"
	case "download":
		verb = "Downloading"
	case "agent", "task":
		return "Delegating"
	case "todos":
		verb = "Updating todos"
	case "question":
		verb = "Asking"
	case "":
		return "Running tool"
	default:
		verb = "Running " + name
		if param == "" {
			return verb
		}
		return verb + " — " + param
	}
	if param == "" {
		return verb
	}
	return verb + " " + param
}

// renderToolSummary renders the per-turn tool call counts and failed
// call total as a footer subtext line, ordered by descending count then
// name so the line is stable across flushes.
func renderToolSummary(counts map[string]int, failures int) string {
	if len(counts) == 0 && failures == 0 {
		return ""
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s ×%d", name, counts[name]))
	}
	if failures > 0 {
		parts = append(parts, fmt.Sprintf("(%d failed)", failures))
	}
	return "-# " + iconTool + " " + strings.Join(parts, " · ")
}

// renderMetricsLine renders context-window usage and the prompt-cache
// hit rate as a footer subtext line, using the same formulas as the TUI
// sidebar (internal/ui/model/sidebar.go, internal/ui/common/elements.go).
func renderMetricsLine(sess session.Session, contextWindow int64) string {
	var parts []string
	if contextWindow > 0 {
		used := sess.PromptTokens + sess.CompletionTokens
		pct := int(float64(used) / float64(contextWindow) * 100)
		label := fmt.Sprintf("context %d%%", pct)
		if sess.EstimatedUsage {
			label = fmt.Sprintf("context ~%d%%", pct)
		}
		parts = append(parts, label)
	}
	if sess.TotalContextTokens > 0 {
		pct := int(float64(sess.TotalCacheReadTokens) / float64(sess.TotalContextTokens) * 100)
		parts = append(parts, fmt.Sprintf("cache %d%%", pct))
	}
	if len(parts) == 0 {
		return ""
	}
	return "-# " + iconMetrics + " " + strings.Join(parts, " · ")
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

// renderAssistantTimeline renders the parts of an assistant message as
// a Discord timeline: thinking markers and streamed markdown text. It
// feeds both the footer's live tail and the frozen per-step transcript;
// tool calls are summarized in the footer instead of listed here.
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
		case message.Finish:
			// Rendered by the status header instead.
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

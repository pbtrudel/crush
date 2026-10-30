package sessionexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// ExportMarkdown renders a session and its messages as a Markdown
// document suitable for sharing or archiving.
func Markdown(sess session.Session, msgs []message.Message) string {
	var b strings.Builder

	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "Untitled Session"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	if sess.ID != "" {
		fmt.Fprintf(&b, "- **Session:** `%s`\n", sess.ID)
	}
	if sess.CreatedAt > 0 {
		fmt.Fprintf(&b, "- **Created:** %s\n", formatTime(sess.CreatedAt))
	}
	if sess.UpdatedAt > 0 {
		fmt.Fprintf(&b, "- **Updated:** %s\n", formatTime(sess.UpdatedAt))
	}
	b.WriteString("\n---\n\n")

	for _, msg := range msgs {
		writeMessage(&b, msg)
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeMessage(b *strings.Builder, msg message.Message) {
	role := messageRoleHeading(msg)
	if role == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", role)

	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.TextContent:
			if p.Hidden || strings.TrimSpace(p.Text) == "" {
				continue
			}
			b.WriteString(strings.TrimSpace(p.Text))
			b.WriteString("\n\n")
		case message.ReasoningContent:
			if strings.TrimSpace(p.Thinking) == "" {
				continue
			}
			b.WriteString("<details><summary>Thinking</summary>\n\n")
			b.WriteString(strings.TrimSpace(p.Thinking))
			b.WriteString("\n\n</details>\n\n")
		case message.ToolCall:
			fmt.Fprintf(b, "**Tool call: `%s`**\n\n", p.Name)
			b.WriteString(fenced("json", prettyJSON(p.Input)))
			b.WriteString("\n\n")
		case message.ToolResult:
			label := "Tool result"
			if p.IsError {
				label = "Tool error"
			}
			fmt.Fprintf(b, "**%s: `%s`**\n\n", label, p.Name)
			if p.Content != "" {
				b.WriteString(fenced("", strings.TrimRight(p.Content, "\n")))
				b.WriteString("\n\n")
			}
			if p.MIMEType != "" {
				fmt.Fprintf(b, "_Attachment: %s_\n\n", p.MIMEType)
			}
		case message.ShellCommand:
			b.WriteString("**Shell command**\n\n")
			b.WriteString(fenced("bash", p.Command))
			b.WriteString("\n\n")
			if p.Output != "" {
				b.WriteString(fenced("", strings.TrimRight(p.Output, "\n")))
				b.WriteString("\n\n")
			}
			if p.ExitCode != 0 {
				fmt.Fprintf(b, "_Exit code: %d_\n\n", p.ExitCode)
			}
		case message.ImageURLContent:
			fmt.Fprintf(b, "![image](%s)\n\n", p.URL)
		case message.BinaryContent:
			fmt.Fprintf(b, "_Image: %s_\n\n", p.Path)
		case message.CompactionStats:
			fmt.Fprintf(b, "_Compacted: %d tokens saved_\n\n", p.SavedTokens)
		}
	}
}

func messageRoleHeading(msg message.Message) string {
	if msg.IsSummaryMessage {
		return "Summary"
	}
	switch msg.Role {
	case message.User:
		return "User"
	case message.Assistant:
		return "Assistant"
	case message.Tool:
		return "Tool result"
	case message.System:
		return "System"
	default:
		return ""
	}
}

func formatTime(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// prettyJSON indents valid JSON for readability and returns the input
// unchanged when it is not valid JSON.
func prettyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// fenced wraps content in a Markdown code fence. The fence is longer
// than any backtick run in the content so embedded fences cannot break
// out of the block.
func fenced(lang, content string) string {
	fence := strings.Repeat("`", max(3, longestBacktickRun(content)+1))
	if lang != "" {
		lang = strings.TrimSpace(lang)
	}
	return fence + lang + "\n" + content + "\n" + fence
}

func longestBacktickRun(s string) int {
	longest, current := 0, 0
	for _, r := range s {
		if r == '`' {
			current++
			longest = max(longest, current)
			continue
		}
		current = 0
	}
	return longest
}

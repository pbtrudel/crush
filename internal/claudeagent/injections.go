package claudeagent

import (
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/logreport"
)

// trailingInjections splits the messages Crush added after a tool result into
// the harness reminders and what the user typed. The CLI reports every
// steered message as sent by the user, so a reminder travels inside the tool
// result instead: the model still reads it, and the transcript does not
// credit the user with text they never wrote.
func trailingInjections(trail fantasy.Prompt) (harness []string, user fantasy.Prompt) {
	for _, msg := range trail {
		if text, ok := injectionText(msg); ok {
			harness = append(harness, text)
			continue
		}
		user = append(user, msg)
	}
	return harness, user
}

// injectionText returns the text of a harness reminder, and false for
// anything else. A reminder is a user message made of text only, opened with
// one of the tags the log report names for the rows the harness writes.
func injectionText(msg fantasy.Message) (string, bool) {
	if msg.Role != fantasy.MessageRoleUser || len(msg.Content) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, part := range msg.Content {
		text, ok := fantasy.AsMessagePart[fantasy.TextPart](part)
		if !ok {
			return "", false
		}
		b.WriteString(text.Text)
	}
	text := b.String()
	if logreport.InjectionTag(text) == "" {
		return "", false
	}
	return text, true
}

// attachInjection appends harness text to a tool result, so the model reads
// the reminder as part of the tool's output.
func attachInjection(result toolResult, text string) toolResult {
	content := slices.Clone(result.content)
	content = append(content, mcpContentBlock{Type: "text", Text: "\n\n" + text})
	return toolResult{content: content, isError: result.isError}
}

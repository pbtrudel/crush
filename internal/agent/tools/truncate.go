package tools

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/shell"
)

// TruncateForTokens keeps a tool result within an estimated token budget by
// retaining its head and tail and writing the complete text to a spill file.
// The estimate is deliberately cheap (four bytes per token), matching the
// rest of Crush's usage accounting. A zero or negative budget disables the
// bound.
func TruncateForTokens(content string, maxTokens int, spillDir string) string {
	if maxTokens <= 0 {
		return content
	}
	return TruncateForChars(content, maxTokens*4, spillDir)
}

// TruncateForChars is TruncateForTokens with an explicit character budget.
// It is also used by the pre-compaction pruner, where the original content
// stays in the session log and no spill file is needed.
func TruncateForChars(content string, maxChars int, spillDir string) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}

	omitted := len(content) - maxChars
	marker := fmt.Sprintf("\n\n[... tool output truncated: %d bytes omitted ...]\n\n", omitted)
	if spillDir != "" {
		if path, err := shell.SpillOutput(content, spillDir); err == nil {
			marker = fmt.Sprintf("\n\n[... tool output truncated: %d bytes omitted, full output: %s ...]\n\n", omitted, path)
		} else {
			slog.Debug("Could not spill tool output", "dir", spillDir, "error", err)
		}
	}

	if maxChars <= len(marker) {
		return runePrefix(marker, maxChars)
	}
	keep := maxChars - len(marker)
	headBudget := keep * 2 / 3
	tailBudget := keep - headBudget
	return runePrefix(content, headBudget) + marker + runeSuffix(content, tailBudget)
}

// runePrefix returns the longest prefix of s within maxBytes without
// splitting a UTF-8 rune.
func runePrefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for idx := range s {
		if idx > maxBytes {
			break
		}
		cut = idx
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
}

// runeSuffix returns the longest suffix of s within maxBytes, starting on a
// rune boundary.
func runeSuffix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// HasSpillNotice reports whether text already carries a truncation marker,
// so layered truncation does not nest notices.
func HasSpillNotice(text string) bool {
	return strings.Contains(text, "[... tool output truncated:")
}

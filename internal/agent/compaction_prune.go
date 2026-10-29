package agent

import (
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// pruneToolResultsForCompaction trims oversized tool results in the
// in-memory copy used to build a compaction request. The original messages
// stay untouched in the session log; the summarizer simply sees less
// repetitive output, which can mean the difference between fitting the
// summary request and overflowing the context window. Results that already
// carry a truncation marker are left alone.
func pruneToolResultsForCompaction(msgs []message.Message, maxChars int) (pruned, saved int) {
	if maxChars <= 0 {
		return 0, 0
	}
	for i := range msgs {
		for j, part := range msgs[i].Parts {
			toolResult, ok := part.(message.ToolResult)
			if !ok || len(toolResult.Content) <= maxChars || tools.HasSpillNotice(toolResult.Content) {
				continue
			}
			trimmed := tools.TruncateForChars(toolResult.Content, maxChars, "")
			saved += len(toolResult.Content) - len(trimmed)
			pruned++
			toolResult.Content = trimmed
			msgs[i].Parts[j] = toolResult
		}
	}
	return pruned, saved
}

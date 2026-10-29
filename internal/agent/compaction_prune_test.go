package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestPruneToolResultsForCompaction(t *testing.T) {
	t.Parallel()

	t.Run("trims oversized tool results", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{
			{Role: message.Tool, Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "1", Name: "bash", Content: strings.Repeat("a", 10_000)},
			}},
			{Role: message.Tool, Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "2", Name: "view", Content: "small"},
			}},
		}
		pruned, saved := pruneToolResultsForCompaction(msgs, 1000)
		require.Equal(t, 1, pruned)
		require.Positive(t, saved)
		require.LessOrEqual(t, len(msgs[0].Parts[0].(message.ToolResult).Content), 1000)
		require.Equal(t, "small", msgs[1].Parts[0].(message.ToolResult).Content)
	})

	t.Run("leaves already truncated results alone", func(t *testing.T) {
		t.Parallel()
		content := "head [... tool output truncated: 5 bytes omitted ...] tail"
		msgs := []message.Message{{Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{Content: content},
		}}}
		pruned, _ := pruneToolResultsForCompaction(msgs, 10)
		require.Zero(t, pruned)
		require.Equal(t, content, msgs[0].Parts[0].(message.ToolResult).Content)
	})

	t.Run("zero disables pruning", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{{Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{Content: strings.Repeat("a", 1000)},
		}}}
		pruned, _ := pruneToolResultsForCompaction(msgs, 0)
		require.Zero(t, pruned)
		require.Len(t, msgs[0].Parts[0].(message.ToolResult).Content, 1000)
	})
}

package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func summaryMessage(parts ...message.ContentPart) *message.Message {
	return &message.Message{
		ID:               "summary-1",
		Role:             message.Assistant,
		IsSummaryMessage: true,
		Parts:            parts,
	}
}

// TestExtractMessageItems_SummaryBecomesStatusLine pins the chat-facing
// half of the compaction UX: a summary message never renders as an
// assistant message, it renders as the single compaction status item.
func TestExtractMessageItems_SummaryBecomesStatusLine(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := summaryMessage(
		message.TextContent{Text: "# Conversation Summary\n\nEverything."},
		message.CompactionStats{TokensBefore: 397088, SummaryTokens: 8192, TailTokens: 22179, SavedTokens: 366717, KeptMessages: 60},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: 42},
	)

	items := ExtractMessageItems(&sty, msg, nil, "/tmp")
	require.Len(t, items, 1)
	_, isCompaction := items[0].(*CompactionItem)
	require.True(t, isCompaction, "summary must render as CompactionItem, got %T", items[0])
}

// TestCompactionItem_RenderStats verifies the finished line shows the
// measured outcome, and that summaries compacted before stats existed
// still render a plain line.
func TestCompactionItem_RenderStats(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("with stats", func(t *testing.T) {
		t.Parallel()
		msg := summaryMessage(
			message.TextContent{Text: "summary"},
			message.CompactionStats{TokensBefore: 397088, SummaryTokens: 8192, TailTokens: 22179, SavedTokens: 366717, KeptMessages: 60},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 42},
		)
		item := NewCompactionItem(&sty, msg)
		require.False(t, item.(*CompactionItem).Spinning())

		rendered := item.Render(100)
		require.Contains(t, rendered, "Context compacted")
		require.Contains(t, rendered, "397,088 → 30,371 tokens")
		require.Contains(t, rendered, "366,717 saved")
		require.Contains(t, rendered, "60 messages kept")
	})

	t.Run("without stats falls back to a plain line", func(t *testing.T) {
		t.Parallel()
		msg := summaryMessage(
			message.TextContent{Text: "summary"},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 42},
		)
		item := NewCompactionItem(&sty, msg)
		rendered := item.Render(100)
		require.Contains(t, rendered, "Context compacted")
		require.NotContains(t, rendered, "tokens")
	})

	t.Run("streaming summary spins", func(t *testing.T) {
		t.Parallel()
		msg := summaryMessage(message.TextContent{Text: ""})
		item := NewCompactionItem(&sty, msg)
		require.True(t, item.(*CompactionItem).Spinning())
		require.False(t, item.(*CompactionItem).Finished())
		rendered := ansi.Strip(item.Render(100))
		require.Contains(t, rendered, "Compacting")
	})

	t.Run("the summary text itself is never rendered", func(t *testing.T) {
		t.Parallel()
		msg := summaryMessage(
			message.TextContent{Text: "# Conversation Summary\n\nSecret handoff details."},
			message.CompactionStats{TokensBefore: 100, SummaryTokens: 10, TailTokens: 20, SavedTokens: 70, KeptMessages: 2},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 42},
		)
		item := NewCompactionItem(&sty, msg)
		rendered := item.Render(100)
		require.NotContains(t, rendered, "Secret handoff")
	})
}

// TestShouldShowAssistantInfo_Summary ensures the compaction line does
// not also grow a model info footer.
func TestShouldShowAssistantInfo_Summary(t *testing.T) {
	t.Parallel()

	msg := summaryMessage(message.Finish{Reason: message.FinishReasonEndTurn, Time: 42})
	require.False(t, ShouldShowAssistantInfo(msg))
}

// TestFormatTokenCount pins the thousands separators.
func TestFormatTokenCount(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0", formatTokenCount(0))
	require.Equal(t, "999", formatTokenCount(999))
	require.Equal(t, "1,000", formatTokenCount(1000))
	require.Equal(t, "397,088", formatTokenCount(397088))
	require.True(t, strings.HasPrefix(formatTokenCount(1234567), "1,234,567"))
}

package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestCompactionsByTrigger(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.session("s2", "title")
	compacted := func(session, trigger string, before int64) {
		log.summary(session, message.TextContent{Text: "summary"},
			message.Finish{Reason: message.FinishReasonEndTurn},
			message.CompactionStats{TokensBefore: before, Trigger: trigger})
	}
	compacted("s1", "guard", 140_000)
	compacted("s1", "guard", 100_000)
	compacted("s1", "threshold", 120_000)
	compacted("s2", "guard", 160_000)
	// Written before triggers were recorded.
	compacted("s2", "", 90_000)
	// Written before compactions recorded statistics: it counts, without
	// tokens before.
	log.summary("s2", message.TextContent{Text: "summary"},
		message.Finish{Reason: message.FinishReasonEndTurn})
	// A failed compaction keeps its summary row, with an error and no
	// statistics; one still being written has neither.
	log.summary("s2", message.Finish{Reason: message.FinishReasonError, Message: "Summarization Error"})
	log.summary("s2", message.TextContent{Text: "partial"})
	// A failed compaction in a child session counts too.
	log.child("s2$$call-compact", "s2")
	log.summary("s2$$call-compact",
		message.Finish{Reason: message.FinishReasonError, Message: "Compaction Error"})
	// Statistics on a message that is not a summary are not a compaction.
	log.add("s2", message.Assistant, message.CompactionStats{TokensBefore: 1})

	require.Equal(t, Compactions{
		Count:              6,
		MedianTokensBefore: 120_000,
		ByTrigger: []Compaction{
			{Trigger: "guard", Count: 3, MedianTokensBefore: 140_000},
			{Trigger: "unstamped", Count: 2, MedianTokensBefore: 90_000},
			{Trigger: "threshold", Count: 1, MedianTokensBefore: 120_000},
		},
		Failed: 2,
	}, log.report(Options{}).Compactions)
}

func TestCompactionsCountUnusedBackgroundSummaries(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	compacted := func(trigger string, before int64) {
		log.summary("s1", message.TextContent{Text: "summary"},
			message.Finish{Reason: message.FinishReasonEndTurn},
			message.CompactionStats{TokensBefore: before, Trigger: trigger})
	}
	compacted("background", 90_000)
	// A summary written ahead of the limit and never adopted compacted
	// nothing, so it is counted apart from the compactions.
	compacted("background_unused", 85_000)
	compacted("background_unused", 88_000)

	require.Equal(t, Compactions{
		Count:              1,
		MedianTokensBefore: 90_000,
		ByTrigger: []Compaction{
			{Trigger: "background", Count: 1, MedianTokensBefore: 90_000},
		},
		UnusedBackground: 2,
	}, log.report(Options{}).Compactions)
}

package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
)

// Compactions is the Compactions section: every finished summary that
// compacted a session, in every session. The medians cover the summaries
// that carry statistics.
type Compactions struct {
	Count              int `json:"count"`
	MedianTokensBefore int `json:"median_tokens_before"`
	// ByTrigger is sorted by count, most first, ties by trigger. A summary
	// written before triggers were recorded reads "unstamped".
	ByTrigger []Compaction `json:"by_trigger"`
	// Failed counts the summaries that ended with an error and carry no
	// statistics. A failed compaction records no trigger.
	Failed int `json:"failed"`
	// UnusedBackground counts the summaries written ahead of the limit
	// that were never adopted. They compacted nothing, so they are not in
	// Count, but each still cost a summary request.
	UnusedBackground int `json:"unused_background"`
}

// Compaction is the compactions one trigger started.
type Compaction struct {
	Trigger            string `json:"trigger"`
	Count              int    `json:"count"`
	MedianTokensBefore int    `json:"median_tokens_before"`
}

type compactionsSection struct {
	// before holds the tokens before each compaction, by trigger.
	before map[string][]int
	// unmeasured counts, by trigger, the compactions written before
	// statistics were recorded; they count but have no tokens before.
	unmeasured       map[string]int
	failed           int
	unusedBackground int
}

func (s *compactionsSection) message(_ *pass, m *message.Message) {
	if !m.IsSummaryMessage {
		return
	}
	stats, ok := m.CompactionStatsPart()
	if !ok {
		if m.FinishReason() == message.FinishReasonError {
			s.failed++
			return
		}
		// A summary with no finish at all is still being written.
		if m.FinishPart() == nil {
			return
		}
		if s.unmeasured == nil {
			s.unmeasured = map[string]int{}
		}
		s.unmeasured["unstamped"]++
		return
	}
	if stats.Trigger == "background_unused" {
		s.unusedBackground++
		return
	}
	trigger := cmp.Or(stats.Trigger, "unstamped")
	s.before[trigger] = append(s.before[trigger], int(stats.TokensBefore))
}

func (s *compactionsSection) endSession(*pass) {}

func (s *compactionsSection) finish(_ *pass, r *Report) {
	c := Compactions{ByTrigger: []Compaction{}, Failed: s.failed, UnusedBackground: s.unusedBackground}
	triggers := map[string]bool{}
	for trigger := range s.before {
		triggers[trigger] = true
	}
	for trigger := range s.unmeasured {
		triggers[trigger] = true
	}
	var all []int
	for trigger := range triggers {
		before := s.before[trigger]
		count := len(before) + s.unmeasured[trigger]
		all = append(all, before...)
		c.Count += count
		c.ByTrigger = append(c.ByTrigger, Compaction{
			Trigger:            trigger,
			Count:              count,
			MedianTokensBefore: percentile(before, 50),
		})
	}
	c.MedianTokensBefore = percentile(all, 50)
	slices.SortFunc(c.ByTrigger, func(a, b Compaction) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Trigger, b.Trigger))
	})
	r.Compactions = c
}

func (c Compactions) writeText(out io.Writer) {
	fmt.Fprintln(out, "Compactions (all sessions)")
	fmt.Fprintf(out, "  compactions\t%d\tmedian tokens before %d\n", c.Count, c.MedianTokensBefore)
	for _, t := range c.ByTrigger {
		fmt.Fprintf(out, "  trigger %s\t%d\tmedian tokens before %d\n", t.Trigger, t.Count, t.MedianTokensBefore)
	}
	fmt.Fprintf(out, "  failed\t%d\n", c.Failed)
	fmt.Fprintf(out, "  unused background summaries\t%d\n", c.UnusedBackground)
	fmt.Fprintln(out)
}

package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
)

// Compactions is the Compactions section: the summaries that carry
// compaction statistics, in every session.
type Compactions struct {
	Count              int `json:"count"`
	MedianTokensBefore int `json:"median_tokens_before"`
	// ByTrigger is sorted by count, most first, ties by trigger. A summary
	// written before triggers were recorded reads "unstamped".
	ByTrigger []Compaction `json:"by_trigger"`
	// Failed counts the summaries that ended with an error and carry no
	// statistics. A failed compaction records no trigger.
	Failed int `json:"failed"`
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
	failed int
}

func (s *compactionsSection) message(_ *pass, m *message.Message) {
	if !m.IsSummaryMessage {
		return
	}
	stats, ok := m.CompactionStatsPart()
	if !ok {
		if m.FinishReason() == message.FinishReasonError {
			s.failed++
		}
		return
	}
	trigger := cmp.Or(stats.Trigger, "unstamped")
	s.before[trigger] = append(s.before[trigger], int(stats.TokensBefore))
}

func (s *compactionsSection) endSession(*pass) {}

func (s *compactionsSection) finish(_ *pass, r *Report) {
	c := Compactions{ByTrigger: []Compaction{}, Failed: s.failed}
	var all []int
	for trigger, before := range s.before {
		all = append(all, before...)
		c.ByTrigger = append(c.ByTrigger, Compaction{
			Trigger:            trigger,
			Count:              len(before),
			MedianTokensBefore: percentile(before, 50),
		})
	}
	c.Count = len(all)
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
	fmt.Fprintln(out)
}

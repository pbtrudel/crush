package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/anim"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// CompactionItem renders a session compaction as a single status line.
// The summary text itself is working state for the model, not chat
// output the user needs to read, so the item shows the measured outcome
// instead: a spinner while the summary is being written, then a
// compact stats line once it lands.
type CompactionItem struct {
	*list.Versioned
	*cachedMessageItem

	message *message.Message
	sty     *styles.Styles
	anim    *anim.Anim
}

var _ Animatable = (*CompactionItem)(nil)

// NewCompactionItem creates a CompactionItem for a summary message. The
// item re-derives everything it renders from the message, so streaming
// updates just replace it via SetMessage.
func NewCompactionItem(sty *styles.Styles, msg *message.Message) MessageItem {
	v := list.NewVersioned()
	c := &CompactionItem{
		Versioned:         v,
		cachedMessageItem: &cachedMessageItem{},
		message:           msg,
		sty:               sty,
	}
	id := msg.ID
	c.anim = anim.New(anim.Settings{
		ID:          id,
		Label:       "Compacting",
		LabelColor:  sty.WorkingLabelColor,
		GradColorA:  sty.WorkingGradFromColor,
		GradColorB:  sty.WorkingGradToColor,
		CycleColors: true,
		NoScramble:  true,
		Suffix: func() string {
			return common.Elapsed(c.message.SessionID)
		},
		SuffixColor: sty.WorkingTimerColor,
	})
	return c
}

// SetMessage swaps in an updated copy of the summary message and
// invalidates the render cache.
func (c *CompactionItem) SetMessage(msg *message.Message) {
	c.message = msg
	c.Bump()
}

// ID implements MessageItem. The summary message's ID keeps live
// updates findable by the same lookup the append path used.
func (c *CompactionItem) ID() string {
	return c.message.ID
}

// FilterValue implements list.Item.
func (c *CompactionItem) FilterValue() string {
	return "Context compacted"
}

// Finished implements list.Item. Unfinished compactions keep the item
// out of the list's freeze cache since the spinner mutates every frame.
func (c *CompactionItem) Finished() bool {
	return c.message.FinishReason() != ""
}

// Spinning implements [Animatable].
func (c *CompactionItem) Spinning() bool {
	return !c.Finished()
}

// restyleAnim implements [animRestyler].
func (c *CompactionItem) restyleAnim() {
	c.anim.SetColors(
		c.sty.WorkingLabelColor,
		c.sty.WorkingGradFromColor,
		c.sty.WorkingGradToColor,
		c.sty.WorkingTimerColor,
	)
}

// Advance implements [Animatable].
func (c *CompactionItem) Advance() bool {
	if !c.Spinning() || !c.anim.Advance() {
		return false
	}
	// Bump the list-cache version so the next draw re-renders this
	// item: a spinner frame mutates anim's internal counter, which
	// changes the rendered output but is invisible to the content
	// cache. Without the bump the spinner would appear frozen.
	c.Bump()
	return true
}

// RawRender implements MessageItem.
func (c *CompactionItem) RawRender(width int) string {
	if c.Spinning() {
		return c.anim.Render()
	}
	return c.renderStats(width)
}

// Render implements MessageItem.
func (c *CompactionItem) Render(width int) string {
	if c.Spinning() {
		// Bypass the cache while spinning: the spinner mutates every
		// animation frame.
		prefix := c.sty.Messages.SectionHeader.Render()
		lines := strings.Split(c.RawRender(width), "\n")
		for i, line := range lines {
			lines[i] = prefix + line
		}
		return strings.Join(lines, "\n")
	}
	if cached, ok := c.getCachedPrefixedRender(width, 0); ok {
		return cached
	}
	prefix := c.sty.Messages.SectionHeader.Render()
	lines := strings.Split(c.RawRender(width), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	out := strings.Join(lines, "\n")
	c.setCachedPrefixedRender(out, width, 0)
	return out
}

// renderStats renders the finished stats line. Summaries compacted before
// the stats part existed (or by an older build) render without numbers
// rather than disappearing from the transcript.
func (c *CompactionItem) renderStats(width int) string {
	if stats, ok := c.message.CompactionStatsPart(); ok {
		after := stats.SummaryTokens + stats.TailTokens
		tokens := fmt.Sprintf("%s → %s tokens · %s saved · %d messages kept",
			formatTokenCount(stats.TokensBefore),
			formatTokenCount(after),
			formatTokenCount(stats.SavedTokens),
			stats.KeptMessages,
		)
		return common.Section(c.sty, "Context compacted", width, tokens)
	}
	return common.Section(c.sty, "Context compacted", width)
}

// formatTokenCount renders a token count with thousands separators.
func formatTokenCount(v int64) string {
	s := fmt.Sprintf("%d", v)
	// Insert separators from the right, three digits at a time.
	var sb strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			sb.WriteByte(',')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

package agent

import (
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
)

// imageExpiredText is the placeholder left in place of an image that aged out of
// the retention window. It tells the model the image is gone so it does not keep
// offering to look at it.
const imageExpiredText = "[Image from an earlier turn omitted: it is no longer in context. Do not reference or offer to verify it.]"

// isUserTurnStart reports whether a stored message begins a real user turn. Hidden
// continuations, compaction summaries, subagent reports, and text-less messages
// (env snapshots, bang-shell output) do not start turns.
func isUserTurnStart(m message.Message) bool {
	if m.Role != message.User || m.IsSummaryMessage {
		return false
	}
	if len(m.SubAgentReports()) > 0 {
		return false
	}
	content := m.Content()
	if content.Hidden {
		return false
	}
	return strings.TrimSpace(content.Text) != ""
}

// imageRetentionCut returns the index into msgs before which image parts are
// expired, keeping images from the last retain user turns. A negative retain
// disables expiry. The result depends only on msgs and retain, so every request
// rebuilds the same bytes.
func imageRetentionCut(msgs []message.Message, retain int) int {
	if retain < 0 {
		return 0
	}
	if retain == 0 {
		return len(msgs)
	}
	var turns []int
	for i, m := range msgs {
		if isUserTurnStart(m) {
			turns = append(turns, i)
		}
	}
	if retain >= len(turns) {
		return 0
	}
	return turns[len(turns)-retain]
}

// compactionRetentionCut returns the cut a compaction request must use to
// extend the last live step's prefix byte-for-byte. The live step replayed the
// history before the final user turn, so the cut is computed on msgs up to that
// turn and then applied to the full msgs. With no real user turn it falls back
// to the ordinary cut.
func compactionRetentionCut(msgs []message.Message, retain int) int {
	last := -1
	for i, m := range msgs {
		if isUserTurnStart(m) {
			last = i
		}
	}
	if last < 0 {
		return imageRetentionCut(msgs, retain)
	}
	return imageRetentionCut(msgs[:last], retain)
}

// omitImagesInMessage replaces image parts in one replayed message with the given
// placeholder text. It does not mutate the input message's backing array.
func omitImagesInMessage(msg fantasy.Message, text string) fantasy.Message {
	content := make([]fantasy.MessagePart, len(msg.Content))
	changed := false
	for j, part := range msg.Content {
		if replaced, ok := omitImagePart(part, text); ok {
			content[j] = replaced
			changed = true
			continue
		}
		content[j] = part
	}
	if changed {
		msg.Content = content
	}
	return msg
}

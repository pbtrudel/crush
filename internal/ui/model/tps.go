package model

import (
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// beginLiveTPS starts the live measurement for a newly created assistant
// step. A finished message (a replay or a late event) does not start one.
func (m *UI) beginLiveTPS(msg message.Message) {
	if msg.Role != message.Assistant || msg.IsSummaryMessage || msg.IsFinished() {
		return
	}
	if m.liveTPSMsgID == msg.ID {
		return
	}
	m.liveTPSMsgID = msg.ID
	m.liveTPSStart = time.Now()
	m.liveTPSBytes = 0
	m.liveTPSTokens = 0
	m.liveTPS = 0
}

// updateLiveTPS advances the live tokens-per-second estimate and folds the
// finished step into the session average.
func (m *UI) updateLiveTPS(msg message.Message) {
	if msg.Role != message.Assistant || msg.IsSummaryMessage {
		return
	}
	if msg.ID != m.liveTPSMsgID {
		m.beginLiveTPS(msg)
		if m.liveTPSMsgID == "" {
			return
		}
	}

	bytes := assistantOutputBytes(msg)
	if bytes > m.liveTPSBytes {
		delta := bytes - m.liveTPSBytes
		m.liveTPSTokens += int64((delta + 3) / 4)
		m.liveTPSBytes = bytes
	}
	if elapsed := time.Since(m.liveTPSStart).Seconds(); elapsed >= 0.25 {
		m.liveTPS = float64(m.liveTPSTokens) / elapsed
	}

	if msg.IsFinished() {
		if m.liveTPSTokens > 0 {
			m.sessionTPSOutput += m.liveTPSTokens
			m.sessionTPSDuration += time.Since(m.liveTPSStart)
		}
		m.liveTPS = 0
		m.liveTPSMsgID = ""
	}
}

// resetSessionTPS seeds the session average from the transcript so the
// sidebar has a number immediately after a session load or switch.
func (m *UI) resetSessionTPS(msgs []message.Message) {
	m.liveTPS = 0
	m.liveTPSMsgID = ""
	m.sessionTPSOutput = 0
	m.sessionTPSDuration = 0
	for _, msg := range msgs {
		if msg.Role != message.Assistant || msg.IsSummaryMessage || msg.IsFinished() == false {
			continue
		}
		bytes := assistantOutputBytes(msg)
		if bytes == 0 || msg.UpdatedAt <= msg.CreatedAt {
			continue
		}
		m.sessionTPSOutput += int64((bytes + 3) / 4)
		m.sessionTPSDuration += time.Duration(max(msg.UpdatedAt-msg.CreatedAt, 1)) * time.Second
	}
}

// tpsStats returns the live and session-average tokens per second.
func (m *UI) tpsStats() (live, average float64) {
	if m.sessionTPSDuration > 0 {
		average = float64(m.sessionTPSOutput) / m.sessionTPSDuration.Seconds()
	}
	return m.liveTPS, average
}

func assistantOutputBytes(msg message.Message) int {
	return len(msg.Content().Text) + len(msg.ReasoningContent().Thinking)
}

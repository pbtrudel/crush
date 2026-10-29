package model

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestUpdateLiveTPS(t *testing.T) {
	t.Parallel()
	m := &UI{}
	msg := message.Message{ID: "a1", Role: message.Assistant}
	m.beginLiveTPS(msg)
	m.liveTPSStart = time.Now().Add(-1 * time.Second)

	msg.Parts = []message.ContentPart{message.TextContent{Text: string(make([]byte, 400))}}
	m.updateLiveTPS(msg)
	require.Positive(t, m.liveTPS)
	require.Positive(t, m.liveTPSTokens)

	msg.Parts = append(msg.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
	m.updateLiveTPS(msg)
	require.Zero(t, m.liveTPS)
	require.Positive(t, m.sessionTPSOutput)
	require.Positive(t, m.sessionTPSDuration)
}

func TestResetSessionTPS(t *testing.T) {
	t.Parallel()
	m := &UI{}
	msgs := []message.Message{
		{
			Role:      message.Assistant,
			CreatedAt: 100,
			UpdatedAt: 102,
			Parts:     []message.ContentPart{message.TextContent{Text: string(make([]byte, 400))}, message.Finish{Reason: message.FinishReasonEndTurn}},
		},
		{
			Role:      message.Assistant,
			CreatedAt: 200,
			UpdatedAt: 204,
			Parts:     []message.ContentPart{message.TextContent{Text: string(make([]byte, 800))}, message.Finish{Reason: message.FinishReasonEndTurn}},
		},
	}
	m.resetSessionTPS(msgs)
	live, average := m.tpsStats()
	require.Zero(t, live)
	require.Positive(t, average)
}

func TestAssistantOutputBytesCountsReasoning(t *testing.T) {
	t.Parallel()
	msg := message.Message{Parts: []message.ContentPart{
		message.TextContent{Text: "abcd"},
		message.ReasoningContent{Thinking: "efgh"},
	}}
	require.Equal(t, 8, assistantOutputBytes(msg))
}

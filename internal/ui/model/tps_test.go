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
	require.Positive(t, m.liveTPS, "the last turn's rate stays visible when idle")
	require.Positive(t, m.sessionTPSOutput)
	require.Positive(t, m.sessionTPSDuration)
}

func TestBeginLiveTPSKeepsPreviousRate(t *testing.T) {
	t.Parallel()
	m := &UI{}
	finished := message.Message{ID: "a1", Role: message.Assistant}
	m.beginLiveTPS(finished)
	m.liveTPSStart = time.Now().Add(-1 * time.Second)
	finished.Parts = []message.ContentPart{message.TextContent{Text: string(make([]byte, 400))}}
	m.updateLiveTPS(finished)
	finished.Parts = append(finished.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
	m.updateLiveTPS(finished)
	last := m.liveTPS
	require.Positive(t, last)

	next := message.Message{ID: "a2", Role: message.Assistant}
	m.beginLiveTPS(next)
	require.Equal(t, last, m.liveTPS, "a new step keeps the previous rate until it has its own estimate")
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

func TestResetSessionTPSFiltersByActiveModel(t *testing.T) {
	t.Parallel()
	m := &UI{}
	msgs := []message.Message{
		{
			Role:      message.Assistant,
			Provider:  "anthropic",
			Model:     "claude",
			CreatedAt: 100,
			UpdatedAt: 102,
			Parts:     []message.ContentPart{message.TextContent{Text: string(make([]byte, 400))}, message.Finish{Reason: message.FinishReasonEndTurn}},
		},
		{
			Role:      message.Assistant,
			Provider:  "openai",
			Model:     "gpt",
			CreatedAt: 200,
			UpdatedAt: 202,
			Parts:     []message.ContentPart{message.TextContent{Text: string(make([]byte, 800))}, message.Finish{Reason: message.FinishReasonEndTurn}},
		},
		{
			Role:             message.Assistant,
			Provider:         "anthropic",
			Model:            "claude",
			IsSummaryMessage: true,
			CreatedAt:        300,
			UpdatedAt:        302,
			Parts:            []message.ContentPart{message.TextContent{Text: string(make([]byte, 400))}, message.Finish{Reason: message.FinishReasonEndTurn}},
		},
	}
	m.resetSessionTPS(msgs)
	_, average := m.tpsStats()
	require.InDelta(t, 100.0, average, 0.001, "only the active model's steps count")
}

func TestResetTPSClearsLiveAndSession(t *testing.T) {
	t.Parallel()
	m := &UI{
		liveTPS:            42,
		liveTPSMsgID:       "a1",
		liveTPSStart:       time.Now(),
		liveTPSBytes:       400,
		liveTPSTokens:      100,
		sessionTPSOutput:   100,
		sessionTPSDuration: time.Second,
	}
	m.resetTPS()
	require.Zero(t, m.liveTPS)
	require.Empty(t, m.liveTPSMsgID)
	require.True(t, m.liveTPSStart.IsZero())
	require.Zero(t, m.liveTPSBytes)
	require.Zero(t, m.liveTPSTokens)
	require.Zero(t, m.sessionTPSOutput)
	require.Zero(t, m.sessionTPSDuration)
}

func TestAssistantOutputBytesCountsReasoning(t *testing.T) {
	t.Parallel()
	msg := message.Message{Parts: []message.ContentPart{
		message.TextContent{Text: "abcd"},
		message.ReasoningContent{Thinking: "efgh"},
	}}
	require.Equal(t, 8, assistantOutputBytes(msg))
}

func TestLiveTPSWaitsForFirstToken(t *testing.T) {
	t.Parallel()
	m := &UI{}
	msg := message.Message{ID: "a1", Role: message.Assistant}
	m.beginLiveTPS(msg)
	require.True(t, m.liveTPSStart.IsZero(), "the decode clock waits for the first token")

	// A tool call start arrives before any output; it must not start the
	// decode clock.
	msg.Parts = []message.ContentPart{message.ToolCall{ID: "t1", Name: "view"}}
	m.updateLiveTPS(msg)
	require.True(t, m.liveTPSStart.IsZero())

	msg.Parts = append(msg.Parts, message.TextContent{Text: string(make([]byte, 400))})
	m.updateLiveTPS(msg)
	require.False(t, m.liveTPSStart.IsZero(), "the first output token starts the clock")

	m.liveTPSStart = time.Now().Add(-1 * time.Second)
	m.updateLiveTPS(msg)
	require.InDelta(t, 100.0, m.liveTPS, 0.001, "rate is tokens over the decode window")
}

func TestSessionAverageUsesDecodeTime(t *testing.T) {
	t.Parallel()
	m := &UI{}
	msgs := []message.Message{
		{
			Role:      message.Assistant,
			CreatedAt: 100,
			UpdatedAt: 110,
			Parts: []message.ContentPart{
				message.TextContent{Text: string(make([]byte, 400))},
				message.Finish{Reason: message.FinishReasonEndTurn, DecodeMs: 2000},
			},
		},
	}
	m.resetSessionTPS(msgs)
	_, average := m.tpsStats()
	require.InDelta(t, 50.0, average, 0.001, "decode time, not wall clock, is the denominator")
}

// routedStep builds a finished assistant step that Hyper/Prism routed to
// the model named by prismID. The router itself is the provider/model.
func routedStep(createdAt int64, prismID string, outputBytes, decodeMs int) message.Message {
	return message.Message{
		Role:           message.Assistant,
		Provider:       "hyper",
		Model:          "prism",
		PrismModelID:   prismID,
		PrismModelName: prismID + "-name",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt + int64(decodeMs/1000),
		Parts: []message.ContentPart{
			message.TextContent{Text: string(make([]byte, outputBytes))},
			message.Finish{Reason: message.FinishReasonEndTurn, DecodeMs: int64(decodeMs)},
		},
	}
}

func TestResetSessionTPSDoesNotMixRoutedModels(t *testing.T) {
	t.Parallel()
	m := &UI{}
	// The router is the same provider and model for both steps; only the
	// routed model differs, so the average must follow the last routed one.
	msgs := []message.Message{
		routedStep(100, "claude-opus", 400, 4000),
		routedStep(200, "gpt-codex", 800, 2000),
	}
	m.resetSessionTPS(msgs)
	_, average := m.tpsStats()
	require.InDelta(t, 100.0, average, 0.001, "only the last routed model's steps count")
}

func TestLiveTPSRestartsAverageWhenRoutedModelChanges(t *testing.T) {
	t.Parallel()
	m := &UI{}
	step := func(id, prismID string, outputBytes, decodeMs int) {
		msg := routedStep(0, prismID, 0, 0)
		msg.ID = id
		msg.Parts = nil
		m.beginLiveTPS(msg)
		msg.Parts = []message.ContentPart{message.TextContent{Text: string(make([]byte, outputBytes))}}
		m.updateLiveTPS(msg)
		msg.Parts = append(msg.Parts, message.Finish{Reason: message.FinishReasonEndTurn, DecodeMs: int64(decodeMs)})
		m.updateLiveTPS(msg)
	}
	step("a1", "claude-opus", 400, 1000)
	step("a2", "gpt-codex", 800, 4000)
	_, average := m.tpsStats()
	require.InDelta(t, 50.0, average, 0.001, "a new routed model restarts the average from its own step")
}

func TestLiveTPSAccumulatesStepsFromOneModel(t *testing.T) {
	t.Parallel()
	m := &UI{}
	step := func(id string, outputBytes, decodeMs int) {
		msg := message.Message{ID: id, Role: message.Assistant, Provider: "anthropic", Model: "claude"}
		m.beginLiveTPS(msg)
		msg.Parts = []message.ContentPart{message.TextContent{Text: string(make([]byte, outputBytes))}}
		m.updateLiveTPS(msg)
		msg.Parts = append(msg.Parts, message.Finish{Reason: message.FinishReasonEndTurn, DecodeMs: int64(decodeMs)})
		m.updateLiveTPS(msg)
	}
	step("a1", 400, 1000)
	step("a2", 800, 4000)
	_, average := m.tpsStats()
	require.InDelta(t, 60.0, average, 0.001, "steps from the same model still pool")
}

package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// redactedThinkingModel streams one Anthropic turn with two reasoning blocks.
// The first carries a redacted thinking block; the second carries none, as
// every block after the redacted one does.
type redactedThinkingModel struct {
	fakeLanguageModel
}

func (m *redactedThinkingModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	parts := []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeReasoningStart, ID: "r1"},
		{Type: fantasy.StreamPartTypeReasoningDelta, ID: "r1", Delta: "first block"},
		{Type: fantasy.StreamPartTypeReasoningEnd, ID: "r1", ProviderMetadata: fantasy.ProviderMetadata{
			anthropic.Name: &anthropic.ReasoningOptionMetadata{Signature: "sig-1", RedactedData: "redacted-x"},
		}},
		{Type: fantasy.StreamPartTypeReasoningStart, ID: "r2"},
		{Type: fantasy.StreamPartTypeReasoningDelta, ID: "r2", Delta: "second block"},
		{Type: fantasy.StreamPartTypeReasoningEnd, ID: "r2", ProviderMetadata: fantasy.ProviderMetadata{
			anthropic.Name: &anthropic.ReasoningOptionMetadata{Signature: "sig-2"},
		}},
		{Type: fantasy.StreamPartTypeTextStart, ID: "t1"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: "done"},
		{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"},
		{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	}
	return func(yield func(fantasy.StreamPart) bool) {
		for _, part := range parts {
			if !yield(part) {
				return
			}
		}
	}, nil
}

// Anthropic needs the redacted thinking block back on the next request. The
// end of a later reasoning block reports no redacted data, and storing that
// empty value must not wipe the block an earlier one carried.
func TestReasoningEndKeepsRedactedThinking(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &redactedThinkingModel{}
	sa := testSessionAgent(env, model, model, "system")

	sess, err := env.sessions.Create(t.Context(), "s")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID:       sess.ID,
		Prompt:          "think first",
		MaxOutputTokens: 1000,
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var reasoning message.ReasoningContent
	found := false
	for _, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for _, part := range msg.Parts {
			if c, ok := part.(message.ReasoningContent); ok {
				reasoning, found = c, true
			}
		}
	}
	require.True(t, found, "the reasoning the model streamed must be stored")
	require.Contains(t, reasoning.Thinking, "first block")
	require.Contains(t, reasoning.Thinking, "second block")
	require.Equal(t, "redacted-x", reasoning.RedactedData,
		"the end of a later block must not drop earlier redacted thinking")
	require.Contains(t, reasoning.Signature, "sig-1",
		"the first block's signature must survive the second block")
}

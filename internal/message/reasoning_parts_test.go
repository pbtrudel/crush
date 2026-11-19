package message

import (
	"reflect"
	"slices"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/require"
)

// reasoningTestResponsesData is the OpenAI Responses reasoning metadata the
// helper tests store and compare.
var reasoningTestResponsesData = openai.ResponsesReasoningMetadata{ItemID: "rs_1"}

// fullReasoningPart returns a reasoning part with every provider field
// set, so a helper that rebuilds the part from a subset of the fields
// loses the rest.
func fullReasoningPart() ReasoningContent {
	return ReasoningContent{
		Thinking:         "plan",
		Signature:        "anthropic-signature",
		RedactedData:     "anthropic-redacted",
		ThoughtSignature: "google-signature",
		ToolID:           "call_1",
		ResponsesData:    &reasoningTestResponsesData,
		StartedAt:        100,
		FinishedAt:       200,
	}
}

// Each reasoning helper owns one field or two and must leave the others
// alone. A helper that rebuilds the part drops the provider data another
// provider stored, and the replayed history then lacks the signatures the
// provider needs.
func TestReasoningHelpersKeepOtherFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		owned []string
		start func() ReasoningContent
		apply func(*Message)
	}{
		{
			name:  "AppendReasoningContent",
			owned: []string{"Thinking"},
			apply: func(m *Message) { m.AppendReasoningContent(" more") },
		},
		{
			name:  "AppendThoughtSignature",
			owned: []string{"ThoughtSignature", "ToolID"},
			apply: func(m *Message) { m.AppendThoughtSignature("-google", "call_1") },
		},
		{
			name:  "AppendReasoningSignature",
			owned: []string{"Signature"},
			apply: func(m *Message) { m.AppendReasoningSignature("-anthropic") },
		},
		{
			name:  "SetReasoningResponsesData",
			owned: []string{"ResponsesData"},
			apply: func(m *Message) { m.SetReasoningResponsesData(&reasoningTestResponsesData) },
		},
		{
			name:  "SetReasoningRedactedData",
			owned: []string{"RedactedData"},
			apply: func(m *Message) { m.SetReasoningRedactedData("anthropic-redacted") },
		},
		{
			name:  "FinishThinking",
			owned: []string{"FinishedAt"},
			start: func() ReasoningContent {
				// FinishThinking only stamps a part that is still open.
				part := fullReasoningPart()
				part.FinishedAt = 0
				return part
			},
			apply: func(m *Message) { m.FinishThinking() },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			start := fullReasoningPart
			if tc.start != nil {
				start = tc.start
			}
			before := start()
			msg := &Message{Role: Assistant, Parts: []ContentPart{before}}
			tc.apply(msg)

			after, ok := msg.Parts[0].(ReasoningContent)
			require.True(t, ok)
			beforeValue := reflect.ValueOf(before)
			afterValue := reflect.ValueOf(after)
			for i := range beforeValue.NumField() {
				name := beforeValue.Type().Field(i).Name
				if slices.Contains(tc.owned, name) {
					continue
				}
				require.Equal(t,
					beforeValue.Field(i).Interface(),
					afterValue.Field(i).Interface(),
					"%s dropped %s", tc.name, name,
				)
			}
		})
	}
}

// OnReasoningEnd runs AppendThoughtSignature and then FinishThinking for a
// Gemini block. Both must keep the thought signature and its tool id, or
// the next turn replays the block without them.
func TestOnReasoningEndSequenceKeepsGoogleData(t *testing.T) {
	t.Parallel()

	msg := &Message{Role: Assistant, Parts: []ContentPart{ReasoningContent{Thinking: "plan"}}}
	msg.AppendThoughtSignature("google-signature", "call_1")
	msg.FinishThinking()

	reasoning := msg.ReasoningContent()
	require.Equal(t, "google-signature", reasoning.ThoughtSignature)
	require.Equal(t, "call_1", reasoning.ToolID)
	require.NotZero(t, reasoning.FinishedAt)

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	part, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](messages[0].Content[0])
	require.True(t, ok)
	metadata, ok := part.ProviderOptions[google.Name].(*google.ReasoningMetadata)
	require.True(t, ok)
	require.Equal(t, "google-signature", metadata.Signature)
	require.Equal(t, "call_1", metadata.ToolID)
}

// Anthropic redacted thinking carries no visible text, only its encrypted
// payload. It must survive storage and replay, or the block cannot be sent
// back on the next turn.
func TestRedactedReasoningRoundTrips(t *testing.T) {
	t.Parallel()

	msg := &Message{Role: Assistant, Parts: []ContentPart{ReasoningContent{RedactedData: "encrypted"}}}
	data, err := EncodeParts(msg.Parts)
	require.NoError(t, err)

	parts, err := DecodeParts(data)
	require.NoError(t, err)
	require.Len(t, parts, 1)

	replayed := (&Message{Role: Assistant, Parts: parts}).ToAIMessage()
	require.Len(t, replayed, 1)
	part, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](replayed[0].Content[0])
	require.True(t, ok)
	metadata, ok := part.ProviderOptions[anthropic.Name].(*anthropic.ReasoningOptionMetadata)
	require.True(t, ok)
	require.Equal(t, "encrypted", metadata.RedactedData)
	require.Empty(t, metadata.Signature)
}

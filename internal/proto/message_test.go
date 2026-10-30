package proto

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageRoleText(t *testing.T) {
	t.Parallel()

	var role MessageRole
	require.NoError(t, role.UnmarshalText([]byte("assistant")))
	assert.Equal(t, Assistant, role)

	b, err := Assistant.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "assistant", string(b))
}

func TestFinishReasonText(t *testing.T) {
	t.Parallel()

	var fr FinishReason
	require.NoError(t, fr.UnmarshalText([]byte("end_turn")))
	assert.Equal(t, FinishReasonEndTurn, fr)

	b, err := FinishReasonMaxTokens.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "max_tokens", string(b))
}

func TestContentPartStrings(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "thinking", ReasoningContent{Thinking: "thinking"}.String())
	assert.Equal(t, "hello", TextContent{Text: "hello"}.String())
	assert.Equal(t, "https://x", ImageURLContent{URL: "https://x"}.String())

	binary := BinaryContent{MIMEType: "image/png", Data: []byte{0x01, 0x02}}
	assert.Equal(t, "AQI=", binary.String(catwalk.InferenceProviderAnthropic))
	assert.Equal(t, "data:image/png;base64,AQI=", binary.String(catwalk.InferenceProviderOpenAI))
}

func TestMessageJSONRoundTrip(t *testing.T) {
	t.Parallel()

	savings := 0.25
	original := Message{
		ID:                      "m1",
		Role:                    Assistant,
		SessionID:               "s1",
		Model:                   "model",
		Provider:                "provider",
		PrismModelID:            "prism",
		PrismModelName:          "prism name",
		PrismHypercreditSavings: &savings,
		CreatedAt:               100,
		UpdatedAt:               200,
		Parts: []ContentPart{
			ReasoningContent{Thinking: "thought", Signature: "sig", StartedAt: 1, FinishedAt: 2},
			TextContent{Text: "answer"},
			ImageURLContent{URL: "https://img", Detail: "high"},
			BinaryContent{Path: "/tmp/f", MIMEType: "text/plain", Data: []byte("data")},
			ToolCall{ID: "t1", Name: "tool", Input: "{}"},
			ToolResult{ToolCallID: "t1", Name: "tool", Content: "done", IsError: true},
			Finish{Reason: FinishReasonEndTurn, Time: 300, Message: "msg", Details: "det"},
			ShellCommand{Command: "ls", Output: "out", ExitCode: 0},
			CompactionStats{TokensBefore: 397088, SummaryTokens: 8192, TailTokens: 22179, SavedTokens: 366717, KeptMessages: 60},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original, decoded)

	// The prism savings pointer must survive a round trip, proving the
	// aliased struct embeds rather than flattens.
	require.NotNil(t, decoded.PrismHypercreditSavings)
	assert.Equal(t, savings, *decoded.PrismHypercreditSavings)
}

func TestMessageUnmarshalJSONInvalidParts(t *testing.T) {
	t.Parallel()

	var m Message
	err := json.Unmarshal([]byte(`{"parts": "not-an-array"}`), &m)
	assert.Error(t, err)
}

func TestMessageAccessors(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{
		ReasoningContent{Thinking: "thought"},
		TextContent{Text: "first"},
		TextContent{Text: "second"},
		ImageURLContent{URL: "https://a"},
		ImageURLContent{URL: "https://b"},
		BinaryContent{Data: []byte("x")},
		ToolCall{ID: "t1"},
		ToolCall{ID: "t2"},
		ToolResult{ToolCallID: "t1"},
		ToolResult{ToolCallID: "t2"},
	}}

	assert.Equal(t, TextContent{Text: "first"}, m.Content())
	assert.Equal(t, ReasoningContent{Thinking: "thought"}, m.ReasoningContent())
	assert.Len(t, m.ImageURLContent(), 2)
	assert.Len(t, m.BinaryContent(), 1)
	assert.Len(t, m.ToolCalls(), 2)
	assert.Len(t, m.ToolResults(), 2)
	assert.False(t, m.IsFinished())
	assert.Nil(t, m.FinishPart())
	assert.Empty(t, m.FinishReason())

	m.AddFinish(FinishReasonEndTurn, "done", "")
	assert.True(t, m.IsFinished())
	require.NotNil(t, m.FinishPart())
	assert.Equal(t, FinishReasonEndTurn, m.FinishReason())

	// Adding a finish part replaces the previous one instead of stacking.
	m.AddFinish(FinishReasonToolUse, "", "")
	assert.Len(t, m.ToolResults(), 2)
	assert.Equal(t, FinishReasonToolUse, m.FinishReason())
}

func TestMessageAccessorsEmpty(t *testing.T) {
	t.Parallel()

	m := Message{}
	assert.Equal(t, TextContent{}, m.Content())
	assert.Equal(t, ReasoningContent{}, m.ReasoningContent())
	assert.Empty(t, m.ImageURLContent())
	assert.Empty(t, m.BinaryContent())
	assert.Empty(t, m.ToolCalls())
	assert.Empty(t, m.ToolResults())
	assert.False(t, m.IsFinished())
	assert.Nil(t, m.FinishPart())
	assert.Empty(t, m.FinishReason())
	assert.False(t, m.IsThinking())
	assert.Zero(t, m.ThinkingDuration())
}

func TestMessageIsThinking(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "hmm"}}}
	assert.True(t, m.IsThinking())

	m.AppendContent("answer")
	assert.False(t, m.IsThinking())

	m2 := Message{Parts: []ContentPart{
		ReasoningContent{Thinking: "hmm"},
		Finish{Reason: FinishReasonEndTurn},
	}}
	assert.False(t, m2.IsThinking())
}

func TestMessageAppendContent(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{TextContent{Text: "a"}, ReasoningContent{Thinking: "r"}}}
	m.AppendContent("b")
	assert.Equal(t, TextContent{Text: "ab"}, m.Content())
	assert.Len(t, m.Parts, 2)

	m2 := Message{}
	m2.AppendContent("x")
	assert.Equal(t, TextContent{Text: "x"}, m2.Content())
}

func TestMessageAppendReasoningContent(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "a", Signature: "s", StartedAt: 5}}}
	m.AppendReasoningContent("b")
	assert.Equal(t, ReasoningContent{Thinking: "ab", Signature: "s", StartedAt: 5}, m.ReasoningContent())

	m2 := Message{}
	m2.AppendReasoningContent("x")
	assert.Equal(t, "x", m2.ReasoningContent().Thinking)
	assert.NotZero(t, m2.ReasoningContent().StartedAt)
}

func TestMessageAppendReasoningSignature(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "a", Signature: "s"}}}
	m.AppendReasoningSignature("+")
	assert.Equal(t, ReasoningContent{Thinking: "a", Signature: "s+"}, m.ReasoningContent())

	m2 := Message{}
	m2.AppendReasoningSignature("sig")
	assert.Empty(t, m2.ReasoningContent().Thinking)
	assert.Equal(t, "sig", m2.ReasoningContent().Signature)
}

func TestMessageFinishThinking(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "a", StartedAt: 10}}}
	m.FinishThinking()
	assert.Equal(t, int64(10), m.ReasoningContent().StartedAt)
	assert.NotZero(t, m.ReasoningContent().FinishedAt)

	// Finishing twice must not overwrite the original timestamp.
	first := m.ReasoningContent().FinishedAt
	m.FinishThinking()
	assert.Equal(t, first, m.ReasoningContent().FinishedAt)

	// No reasoning part: no-op.
	m2 := Message{Parts: []ContentPart{TextContent{Text: "x"}}}
	m2.FinishThinking()
	assert.Len(t, m2.Parts, 1)
}

func TestMessageThinkingDuration(t *testing.T) {
	t.Parallel()

	finished := Message{Parts: []ContentPart{ReasoningContent{}}}
	assert.Zero(t, finished.ThinkingDuration())

	m := Message{Parts: []ContentPart{ReasoningContent{StartedAt: 10, FinishedAt: 35}}}
	assert.Equal(t, 25*time.Second, m.ThinkingDuration())

	open := Message{Parts: []ContentPart{ReasoningContent{StartedAt: time.Now().Unix() - 60}}}
	assert.GreaterOrEqual(t, open.ThinkingDuration(), 59*time.Second)
}

func TestMessageToolCallMutations(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{ToolCall{ID: "t1", Name: "n", Input: "{"}}}

	m.AppendToolCallInput("t1", "}")
	tc := m.ToolCalls()[0]
	assert.Equal(t, "{}", tc.Input)
	assert.False(t, tc.Finished)

	m.FinishToolCall("t1")
	assert.True(t, m.ToolCalls()[0].Finished)

	// Unknown ids are no-ops.
	m.AppendToolCallInput("nope", "x")
	m.FinishToolCall("nope")
	assert.Len(t, m.ToolCalls(), 1)

	m.AddToolCall(ToolCall{ID: "t1", Name: "replaced"})
	assert.Equal(t, "replaced", m.ToolCalls()[0].Name)

	m.AddToolCall(ToolCall{ID: "t2"})
	assert.Len(t, m.ToolCalls(), 2)

	m.AddToolResult(ToolResult{ToolCallID: "t1"})
	assert.Len(t, m.ToolResults(), 1)

	// SetToolResults appends rather than replaces, despite its name; pin
	// that behavior so a future rename to real replacement is deliberate.
	m.SetToolResults([]ToolResult{{ToolCallID: "t2"}, {ToolCallID: "t3"}})
	results := m.ToolResults()
	require.Len(t, results, 3)
	assert.Equal(t, "t1", results[0].ToolCallID)
	assert.Equal(t, "t2", results[1].ToolCallID)
	assert.Equal(t, "t3", results[2].ToolCallID)
}

func TestMessageSetToolCalls(t *testing.T) {
	t.Parallel()

	m := Message{Parts: []ContentPart{
		TextContent{Text: "keep"},
		ToolCall{ID: "t1"},
		ReasoningContent{Thinking: "keep"},
		ToolCall{ID: "t2"},
	}}
	m.SetToolCalls([]ToolCall{{ID: "t3"}, {ID: "t4"}})

	parts := m.Parts
	assert.Len(t, parts, 4)
	assert.IsType(t, TextContent{}, parts[0])
	assert.IsType(t, ReasoningContent{}, parts[1])
	assert.Equal(t, ToolCall{ID: "t3"}, parts[2])
	assert.Equal(t, ToolCall{ID: "t4"}, parts[3])
}

func TestMessageAddImageURLAndBinary(t *testing.T) {
	t.Parallel()

	m := Message{}
	m.AddImageURL("https://img", "low")
	m.AddBinary("image/png", []byte{1})

	assert.Equal(t, ImageURLContent{URL: "https://img", Detail: "low"}, m.ImageURLContent()[0])
	assert.Equal(t, "image/png", m.BinaryContent()[0].MIMEType)
}

func TestMarshalPartsUnknownType(t *testing.T) {
	t.Parallel()

	_, err := MarshalParts([]ContentPart{unknownPart{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown part type")
}

type unknownPart struct{}

func (unknownPart) isPart() {}

func TestPartsRoundTrip(t *testing.T) {
	t.Parallel()

	parts := []ContentPart{
		ReasoningContent{Thinking: "t", Signature: "s"},
		TextContent{Text: "txt", Hidden: true},
		ImageURLContent{URL: "u", Detail: "d"},
		BinaryContent{MIMEType: "m", Data: []byte("b")},
		ToolCall{ID: "1", Name: "n", Input: "i", Type: "function", Finished: true},
		ToolResult{ToolCallID: "1", Name: "n", Content: "c", Data: "d", MIMEType: "m", Metadata: "{}", IsError: true},
		Finish{Reason: FinishReasonCanceled, Time: 1},
		ShellCommand{Command: "c", Output: "o", ExitCode: 2},
	}

	data, err := MarshalParts(parts)
	require.NoError(t, err)
	decoded, err := UnmarshalParts(data)
	require.NoError(t, err)
	assert.Equal(t, parts, decoded)
}

func TestUnmarshalPartsErrors(t *testing.T) {
	t.Parallel()

	_, err := UnmarshalParts([]byte("not json"))
	assert.Error(t, err)

	_, err = UnmarshalParts([]byte(`[{"type": "nope", "data": {}}]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown part type")

	_, err = UnmarshalParts([]byte(`[{"type": "text", "data": "scalar"}]`))
	assert.Error(t, err)
}

func TestAttachmentJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := Attachment{FilePath: "/f", FileName: "f.txt", MimeType: "text/plain", Content: []byte("hello")}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"content":"aGVsbG8="`)

	var decoded Attachment
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original, decoded)

	err = json.Unmarshal([]byte(`{"content": "!!!"}`), &decoded)
	assert.Error(t, err)
}

func TestAttachmentConversions(t *testing.T) {
	t.Parallel()

	att := Attachment{FilePath: "/f", FileName: "f.txt", MimeType: "text/plain", Content: []byte("x")}
	msgAtt := att.ToMessage()
	assert.Equal(t, att.FilePath, msgAtt.FilePath)
	assert.Equal(t, att.Content, msgAtt.Content)

	back := AttachmentFromMessage(msgAtt)
	assert.Equal(t, att, back)

	pairs := AttachmentsToMessage([]Attachment{att, back})
	assert.Len(t, pairs, 2)
	fromMsg := AttachmentsFromMessage([]message.Attachment{msgAtt})
	assert.Equal(t, []Attachment{att}, fromMsg)
}

func TestAgentEventTypeText(t *testing.T) {
	t.Parallel()

	var typ AgentEventType
	require.NoError(t, typ.UnmarshalText([]byte("error")))
	assert.Equal(t, AgentEventTypeError, typ)

	b, err := AgentEventTypeResponse.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "response", string(b))
}

func TestAgentEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentEvent{
		Type:     AgentEventTypeError,
		Error:    errors.New("boom"),
		RunID:    "run-1",
		Done:     true,
		Progress: "working",
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"error":"boom"`)

	var decoded AgentEvent
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original.Type, decoded.Type)
	require.Error(t, decoded.Error)
	assert.Equal(t, "boom", decoded.Error.Error())
	assert.Equal(t, "run-1", decoded.RunID)
	assert.True(t, decoded.Done)

	noError := AgentEvent{Type: AgentEventTypeResponse}
	data, err = json.Marshal(noError)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"error"`)

	decoded = AgentEvent{}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.NoError(t, decoded.Error)
}

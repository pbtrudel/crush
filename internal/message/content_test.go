package message

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func makeTestAttachments(n int, contentSize int) []Attachment {
	attachments := make([]Attachment, n)
	content := []byte(strings.Repeat("x", contentSize))
	for i := range n {
		attachments[i] = Attachment{
			FilePath: fmt.Sprintf("/path/to/file%d.txt", i),
			MimeType: "text/plain",
			Content:  content,
		}
	}
	return attachments
}

func TestToAIMessage_CorruptedMediaData(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_123",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "abc\x80def",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_123", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "corrupted media should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func TestToAIMessage_ValidMediaData(t *testing.T) {
	t.Parallel()

	validBase64 := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47})

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_456",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       validBase64,
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_456", part.ToolCallID)

	mediaContent, ok := part.Output.(fantasy.ToolResultOutputContentMedia)
	require.True(t, ok, "valid media should remain as media")
	require.Equal(t, validBase64, mediaContent.Data)
	require.Equal(t, "image/png", mediaContent.MediaType)
}

func TestToAIMessage_ASCIIButInvalidBase64(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_789",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "not-valid-base64!!!",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_789", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "ASCII but invalid base64 should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func BenchmarkPromptWithTextAttachments(b *testing.B) {
	cases := []struct {
		name        string
		numFiles    int
		contentSize int
	}{
		{"1file_100bytes", 1, 100},
		{"5files_1KB", 5, 1024},
		{"10files_10KB", 10, 10 * 1024},
		{"20files_50KB", 20, 50 * 1024},
	}

	for _, tc := range cases {
		attachments := makeTestAttachments(tc.numFiles, tc.contentSize)
		prompt := "Process these files"

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = PromptWithTextAttachments(prompt, attachments)
			}
		})
	}
}

func TestResetStreamedContent(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.AddImageURL("https://example.com/img.png", "high")
	msg.AppendContent("partial answer")
	msg.AppendReasoningContent("thinking...")
	msg.AddToolCall(ToolCall{ID: "1", Name: "bash"})
	msg.AddToolResult(ToolResult{ToolCallID: "1", Content: "output"})
	msg.AddFinish(FinishReasonError, "boom", "stream died")

	msg.ResetStreamedContent()

	// Streamed parts are gone.
	require.Empty(t, msg.Content().Text, "text should be cleared")
	require.Empty(t, msg.ReasoningContent().Thinking, "reasoning should be cleared")
	require.Empty(t, msg.ToolCalls(), "tool calls should be cleared")
	require.Nil(t, msg.FinishPart(), "finish should be cleared")

	// Non-streamed parts survive.
	require.Len(t, msg.ImageURLContent(), 1, "image should survive")
	require.Len(t, msg.ToolResults(), 1, "tool results should survive")
}

func TestTruncateReasoning(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.AppendReasoningContent("thinking about the problem")
	msg.AppendContent("answer text")

	msg.TruncateReasoning(len([]rune("thinking")))

	require.Equal(t, "thinking", msg.ReasoningContent().Thinking)
	require.Equal(t, "answer text", msg.Content().Text)
}

func TestResetStreamedContentEmpty(t *testing.T) {
	t.Parallel()

	// Reset on an empty message is a no-op and must not panic.
	msg := &Message{}
	msg.ResetStreamedContent()
	require.Empty(t, msg.Parts)
}

func TestAddFinishRecordsDecodeTime(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.SetFirstTokenAt(time.Now().Add(-2 * time.Second))
	msg.AddFinish(FinishReasonEndTurn, "", "")

	finish := msg.FinishPart()
	require.NotNil(t, finish)
	require.InDelta(t, 2000, finish.DecodeMs, 100)
}

func TestSetFirstTokenAtKeepsTheFirstToken(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.SetFirstTokenAt(time.Now().Add(-3 * time.Second))
	// Later deltas must not move the decode start.
	msg.SetFirstTokenAt(time.Now())
	msg.AddFinish(FinishReasonEndTurn, "", "")

	require.InDelta(t, 3000, msg.FinishPart().DecodeMs, 100)
}

func TestAddFinishWithoutTokensHasNoDecodeTime(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.AddFinish(FinishReasonError, "boom", "")

	require.Zero(t, msg.FinishPart().DecodeMs)
}

func TestAddFinishAtClampsDecodeTime(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.SetFirstTokenAt(time.Now())
	// A backdated finish that precedes the first token reports no decode
	// time rather than a negative one.
	msg.AddFinishAt(FinishReasonError, "", "", time.Now().Add(-time.Minute).Unix())

	require.Zero(t, msg.FinishPart().DecodeMs)
}

func TestParseSubAgentReportPromptRoundTrips(t *testing.T) {
	t.Parallel()

	want := SubAgentReport{Label: `odd "label"`, Output: "line one\n</subagent-report>\nline three"}
	got, ok := ParseSubAgentReportPrompt(want.PromptText())
	require.True(t, ok)
	require.Equal(t, want, got)

	_, ok = ParseSubAgentReportPrompt("just a prompt")
	require.False(t, ok)
}

func TestToAIMessage_SignedReasoningReplayed(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Assistant,
		Parts: []ContentPart{
			ReasoningContent{Signature: "sig"},
			TextContent{Text: "hello"},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 2)

	reasoning, ok := messages[0].Content[0].(fantasy.ReasoningPart)
	require.True(t, ok)
	require.Empty(t, reasoning.Text)
	require.NotNil(t, reasoning.ProviderOptions)

	// Anthropic rejects blank text blocks; signed reasoning must survive.
	msg.Parts[1] = TextContent{Text: " \t\n\u2003"}
	replayed := msg.ToAIMessage()
	require.Len(t, replayed, 1)
	require.Equal(t, messages[0].Content[:1], replayed[0].Content)
}

func TestToAIMessage_UserContextReplayed(t *testing.T) {
	t.Parallel()

	// The stored text is already normalized at creation, so replay must
	// prepend the context and keep the prompt byte-for-byte.
	msg := &Message{
		Role: User,
		Parts: []ContentPart{
			TextContent{Text: "continue", Context: "<host_context>x</host_context>\n\n"},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	part, ok := messages[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, "<host_context>x</host_context>\n\ncontinue", part.Text)

	// Omit blank replay text only after composing the stored context.
	content := TextContent{Text: " \t\n\u2003", Context: "<host_context>x</host_context>\n\n"}
	msg.Parts[0] = content
	messages = msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Equal(t, []fantasy.MessagePart{fantasy.TextPart{Text: content.Context + content.Text}}, messages[0].Content)
	require.Equal(t, content, msg.Content())

	msg.Parts[0] = TextContent{Text: content.Text}
	messages = msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Empty(t, messages[0].Content)
}

func TestAppendContentKeepsContext(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{TextContent{Text: "a", Hidden: true, Context: "ctx"}}}
	msg.AppendContent("b")
	require.Equal(t, TextContent{Text: "ab", Hidden: true, Context: "ctx"}, msg.Content())
}

// requestUsage builds a fully populated usage record, so a round trip that
// drops any one field fails.
func requestUsage() RequestUsage {
	return RequestUsage{
		InputTokens:      1_200,
		CacheReadTokens:  9_000,
		CacheWriteTokens: 3_000,
		OutputTokens:     450,
		PromptMessages:   12,
		PreviousMessages: 10,
		SharedMessages:   10,
		ToolsChanged:     true,
		Estimated:        true,
	}
}

func TestFinishRequestUsageRoundTrip(t *testing.T) {
	t.Parallel()

	usage := requestUsage()
	encoded, err := marshalParts([]ContentPart{Finish{Reason: FinishReasonEndTurn, Time: 7, Usage: &usage}})
	require.NoError(t, err)

	decoded, err := unmarshalParts(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	finish, ok := decoded[0].(Finish)
	require.True(t, ok)
	require.NotNil(t, finish.Usage)
	require.Equal(t, usage, *finish.Usage)
}

func TestFinishWithoutUsageHasNoUsageKey(t *testing.T) {
	t.Parallel()

	encoded, err := marshalParts([]ContentPart{Finish{Reason: FinishReasonEndTurn, Time: 7}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "usage")
}

func TestSetRequestUsage(t *testing.T) {
	t.Parallel()

	usage := requestUsage()

	t.Run("records on the finish part", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetRequestUsage(usage)

		finish, ok := msg.Parts[len(msg.Parts)-1].(Finish)
		require.True(t, ok)
		require.NotNil(t, finish.Usage)
		require.Equal(t, usage, *finish.Usage)
	})

	t.Run("is a no-op without a finish part", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant, Parts: []ContentPart{TextContent{Text: "hi"}}}
		msg.SetRequestUsage(usage)
		require.Equal(t, []ContentPart{TextContent{Text: "hi"}}, msg.Parts)
	})
}

func TestSetBuild(t *testing.T) {
	t.Parallel()

	t.Run("records on the finish part and keeps the usage", func(t *testing.T) {
		t.Parallel()
		usage := requestUsage()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetRequestUsage(usage)
		msg.SetBuild("0.100.0-fork.100")

		finish := msg.FinishPart()
		require.NotNil(t, finish)
		require.Equal(t, "0.100.0-fork.100", finish.Build)
		require.NotNil(t, finish.Usage)
		require.Equal(t, usage, *finish.Usage)
	})

	t.Run("is a no-op without a finish part", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant, Parts: []ContentPart{TextContent{Text: "hi"}}}
		msg.SetBuild("0.100.0-fork.100")
		require.Equal(t, []ContentPart{TextContent{Text: "hi"}}, msg.Parts)
	})

	t.Run("a later finish drops the stamp", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetBuild("0.100.0-fork.100")
		msg.AddFinish(FinishReasonError, "Provider Error", "boom")
		require.Empty(t, msg.FinishPart().Build, "callers stamp after the last AddFinish")
	})
}

func TestFinishWithoutBuildHasNoBuildKey(t *testing.T) {
	t.Parallel()

	encoded, err := marshalParts([]ContentPart{Finish{Reason: FinishReasonEndTurn, Time: 7}, CompactionStats{TokensBefore: 1}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"build"`)
	require.NotContains(t, string(encoded), `"trigger"`)
}

func TestSetTurnEnd(t *testing.T) {
	t.Parallel()

	t.Run("records on the finish part and keeps the build", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetBuild("0.100.0-fork.100")
		msg.SetTurnEnd(TurnEndCompleted)

		finish := msg.FinishPart()
		require.NotNil(t, finish)
		require.Equal(t, TurnEndCompleted, finish.TurnEnd)
		require.Equal(t, "0.100.0-fork.100", finish.Build)
	})

	t.Run("is a no-op without a finish part", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant, Parts: []ContentPart{TextContent{Text: "hi"}}}
		msg.SetTurnEnd(TurnEndCompleted)
		require.Equal(t, []ContentPart{TextContent{Text: "hi"}}, msg.Parts)
	})
}

func TestFinishWithoutTurnEndHasNoTurnEndKey(t *testing.T) {
	t.Parallel()

	encoded, err := marshalParts([]ContentPart{Finish{Reason: FinishReasonEndTurn, Time: 7}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"turn_end"`)
}

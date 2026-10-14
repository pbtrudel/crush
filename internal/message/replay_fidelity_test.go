package message

import (
	"encoding/base64"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// The replay path must not normalize stored text. A trim here changes the
// bytes sent on the next turn compared with the live request, which
// invalidates the provider's prompt cache for the whole conversation.
func TestToAIMessage_KeepsTextVerbatim(t *testing.T) {
	t.Parallel()

	user := &Message{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "  hello\n\n"}},
	}
	userMessages := user.ToAIMessage()
	require.Len(t, userMessages, 1)
	userText, ok := fantasy.AsMessagePart[fantasy.TextPart](userMessages[0].Content[0])
	require.True(t, ok)
	require.Equal(t, "  hello\n\n", userText.Text)

	assistant := &Message{
		Role:  Assistant,
		Parts: []ContentPart{TextContent{Text: "\nAnswer\n"}},
	}
	assistantMessages := assistant.ToAIMessage()
	require.Len(t, assistantMessages, 1)
	assistantText, ok := fantasy.AsMessagePart[fantasy.TextPart](assistantMessages[0].Content[0])
	require.True(t, ok)
	require.Equal(t, "\nAnswer\n", assistantText.Text)
}

// A media tool result replayed from the session store must carry the same
// caption the live request sent: Anthropic serializes it as a text block next
// to the image, so dropping it changes the request bytes.
func TestToAIMessage_MediaKeepsCaption(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_1",
				Name:       "screenshot",
				Content:    "a caption",
				Data:       base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47}),
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)
	media, ok := part.Output.(fantasy.ToolResultOutputContentMedia)
	require.True(t, ok)
	require.Equal(t, "a caption", media.Text)
}

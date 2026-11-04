package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func imageBudgetFile(name, mediaType string) fantasy.FilePart {
	return fantasy.FilePart{
		Filename:  name,
		MediaType: mediaType,
		Data:      []byte("data"),
	}
}

func imageBudgetMedia(id, mediaType string) fantasy.ToolResultPart {
	return fantasy.ToolResultPart{
		ToolCallID: id,
		Output: fantasy.ToolResultOutputContentMedia{
			Data:      "ZGF0YQ==",
			MediaType: mediaType,
			Text:      "tool image",
		},
	}
}

func imageBudgetMessage(parts ...fantasy.MessagePart) fantasy.Message {
	return fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: parts,
	}
}

func countImageBudgetParts(messages []fantasy.Message) (files, media int) {
	for _, msg := range messages {
		for _, part := range msg.Content {
			if file, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok && isImageMediaType(file.MediaType) {
				files++
			}
			if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				if mediaPart, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output); ok && isImageMediaType(mediaPart.MediaType) {
					media++
				}
			}
		}
	}
	return files, media
}

func TestApplyImageBudget(t *testing.T) {
	t.Parallel()

	t.Run("under the limit keeps every image", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(fantasy.TextPart{Text: "one"}, imageBudgetFile("a.png", "image/png")),
			imageBudgetMessage(imageBudgetFile("b.png", "image/png")),
		}
		got := applyImageBudget(messages, 3)
		files, media := countImageBudgetParts(got)
		require.Equal(t, 2, files)
		require.Zero(t, media)
	})

	t.Run("over the limit omits the oldest images", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(imageBudgetFile("oldest.png", "image/png")),
			imageBudgetMessage(imageBudgetFile("middle.png", "image/png")),
			imageBudgetMessage(imageBudgetFile("newest.png", "image/png")),
		}
		got := applyImageBudget(messages, 2)
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 2, files, "the two newest images must remain")

		first := got[0].Content[0]
		text, ok := fantasy.AsMessagePart[fantasy.TextPart](first)
		require.True(t, ok, "the omitted image must become a text placeholder")
		require.Contains(t, text.Text, "Image omitted")
	})

	t.Run("tool result media counts and is omitted", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(imageBudgetMedia("call-1", "image/png")),
			imageBudgetMessage(imageBudgetFile("newest.png", "image/png")),
		}
		got := applyImageBudget(messages, 1)
		_, media := countImageBudgetParts(got)
		require.Zero(t, media, "the older tool image must be omitted")
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 1, files, "the newest image must remain")

		result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got[0].Content[0])
		require.True(t, ok)
		output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output)
		require.True(t, ok)
		require.Contains(t, output.Text, "Image omitted")
		require.Contains(t, output.Text, "tool image")
	})

	t.Run("non-image files are not counted", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(imageBudgetFile("notes.txt", "text/plain")),
			imageBudgetMessage(imageBudgetFile("voice.mp3", "audio/mpeg")),
			imageBudgetMessage(imageBudgetFile("photo.png", "image/png")),
		}
		got := applyImageBudget(messages, 1)
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 1, files)
		_, isText := fantasy.AsMessagePart[fantasy.TextPart](got[0].Content[0])
		require.False(t, isText, "a non-image file must not be replaced")
	})

	t.Run("zero disables the budget", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(imageBudgetFile("a.png", "image/png")),
			imageBudgetMessage(imageBudgetFile("b.png", "image/png")),
		}
		got := applyImageBudget(messages, 0)
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 2, files)
	})

	t.Run("multiple images in one message", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(
				imageBudgetFile("first.png", "image/png"),
				imageBudgetFile("second.png", "image/png"),
				imageBudgetFile("third.png", "image/png"),
			),
		}
		got := applyImageBudget(messages, 2)
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 2, files)
		_, firstIsText := fantasy.AsMessagePart[fantasy.TextPart](got[0].Content[0])
		require.True(t, firstIsText)
		_, secondIsFile := fantasy.AsMessagePart[fantasy.FilePart](got[0].Content[1])
		require.True(t, secondIsFile)
	})

	t.Run("input messages are not mutated", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			imageBudgetMessage(imageBudgetFile("a.png", "image/png")),
			imageBudgetMessage(imageBudgetFile("b.png", "image/png")),
		}
		_ = applyImageBudget(messages, 1)
		files, _ := countImageBudgetParts(messages)
		require.Equal(t, 2, files, "the caller's messages must keep their images")
	})
}

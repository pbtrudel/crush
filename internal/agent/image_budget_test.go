package agent

import (
	"fmt"
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

func imageBudgetMessages(n int) []fantasy.Message {
	messages := make([]fantasy.Message, n)
	for i := range n {
		messages[i] = imageBudgetMessage(imageBudgetFile(
			fmt.Sprintf("image-%02d.png", i),
			"image/png",
		))
	}
	return messages
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

	t.Run("quantizes the omitted count so the cut stays stable", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			total int
			kept  int
		}{
			{total: 10, kept: 10},
			{total: 11, kept: 9},
			{total: 12, kept: 10},
			{total: 13, kept: 9},
			{total: 14, kept: 10},
			{total: 15, kept: 9},
		}
		for _, tc := range cases {
			got := applyImageBudget(imageBudgetMessages(tc.total), 10)
			files, _ := countImageBudgetParts(got)
			require.Equalf(t, tc.kept, files, "limit 10 with %d images", tc.total)
		}

		got := applyImageBudget(imageBudgetMessages(11), 10)
		for i := range 2 {
			_, isText := fantasy.AsMessagePart[fantasy.TextPart](got[i].Content[0])
			require.Truef(t, isText, "image %d must be omitted", i)
		}
		for i := 2; i < 11; i++ {
			_, isFile := fantasy.AsMessagePart[fantasy.FilePart](got[i].Content[0])
			require.Truef(t, isFile, "image %d must be kept", i)
		}
	})

	t.Run("the cut holds still while fewer than a step of images arrive", func(t *testing.T) {
		t.Parallel()
		eleven := imageBudgetMessages(11)
		twelve := append(imageBudgetMessages(11), imageBudgetMessage(imageBudgetFile("image-11.png", "image/png")))

		first := applyImageBudget(eleven, 10)
		second := applyImageBudget(twelve, 10)

		require.Len(t, second, 12)
		require.Equal(t, first, second[:len(first)], "the shared prefix must stay byte-identical")

		thirteen := imageBudgetMessages(13)
		require.NotEqual(t, first, applyImageBudget(thirteen, 10)[:len(first)], "the cut must move once a full step arrives")
	})

	t.Run("limit one omits every older image", func(t *testing.T) {
		t.Parallel()
		got := applyImageBudget(imageBudgetMessages(4), 1)
		files, _ := countImageBudgetParts(got)
		require.Equal(t, 1, files)
		file, ok := fantasy.AsMessagePart[fantasy.FilePart](got[3].Content[0])
		require.True(t, ok, "the newest image must be kept")
		require.Equal(t, "image-03.png", file.Filename)
		_, isText := fantasy.AsMessagePart[fantasy.TextPart](got[0].Content[0])
		require.True(t, isText, "the oldest image must be omitted")
	})

	t.Run("never keeps more than the limit or omits more than the total", func(t *testing.T) {
		t.Parallel()
		for _, limit := range []int{1, 2, 3, 5, 10, 11, 100} {
			for total := range 131 {
				got := applyImageBudget(imageBudgetMessages(total), limit)
				files, _ := countImageBudgetParts(got)
				require.Len(t, got, total)
				require.LessOrEqualf(t, files, limit, "limit %d with %d images kept %d", limit, total, files)
				require.GreaterOrEqualf(t, files, 0, "limit %d with %d images", limit, total)
				require.LessOrEqualf(t, total-files, total, "limit %d with %d images omitted too many", limit, total)
			}
		}
	})
}

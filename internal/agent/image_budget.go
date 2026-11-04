package agent

import (
	"strings"

	"charm.land/fantasy"
)

// imageOmittedText is the placeholder left in place of an image dropped by
// the request image budget.
const imageOmittedText = "[Image omitted to stay within the request image limit]"

// isImageMediaType reports whether a media type is an image the model can
// see. Audio, text, and other file parts do not count against the budget.
func isImageMediaType(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(mediaType), "image/")
}

// applyImageBudget keeps at most limit images in a request, replacing the
// oldest ones with text placeholders. The newest images are kept because
// they are the ones the current turn is about. A limit of zero or less
// disables the budget. The caller's messages are not mutated.
func applyImageBudget(messages []fantasy.Message, limit int) []fantasy.Message {
	if limit <= 0 {
		return messages
	}
	toOmit := countImages(messages) - limit
	if toOmit <= 0 {
		return messages
	}

	out := make([]fantasy.Message, len(messages))
	for i, msg := range messages {
		if toOmit == 0 {
			out[i] = msg
			continue
		}
		content := make([]fantasy.MessagePart, len(msg.Content))
		changed := false
		for j, part := range msg.Content {
			if toOmit > 0 {
				if replaced, ok := omitImagePart(part); ok {
					content[j] = replaced
					toOmit--
					changed = true
					continue
				}
			}
			content[j] = part
		}
		if changed {
			msg.Content = content
		}
		out[i] = msg
	}
	return out
}

// countImages counts the image parts in a prompt in chronological order.
func countImages(messages []fantasy.Message) int {
	count := 0
	for _, msg := range messages {
		for _, part := range msg.Content {
			if isImagePart(part) {
				count++
			}
		}
	}
	return count
}

// isImagePart reports whether a message part carries an image.
func isImagePart(part fantasy.MessagePart) bool {
	if file, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
		return isImageMediaType(file.MediaType)
	}
	if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
		if media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output); ok {
			return isImageMediaType(media.MediaType)
		}
	}
	return false
}

// omitImagePart replaces an image part with a text placeholder. It reports
// false for parts that do not carry an image.
func omitImagePart(part fantasy.MessagePart) (fantasy.MessagePart, bool) {
	if file, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
		if !isImageMediaType(file.MediaType) {
			return nil, false
		}
		return fantasy.TextPart{Text: imageOmittedText, ProviderOptions: file.ProviderOptions}, true
	}
	if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
		media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output)
		if !ok || !isImageMediaType(media.MediaType) {
			return nil, false
		}
		text := imageOmittedText
		if media.Text != "" {
			text = media.Text + "\n" + imageOmittedText
		}
		return fantasy.ToolResultPart{
			ToolCallID:       result.ToolCallID,
			Output:           fantasy.ToolResultOutputContentText{Text: text},
			ProviderExecuted: result.ProviderExecuted,
			ProviderOptions:  result.ProviderOptions,
			ClientMetadata:   result.ClientMetadata,
		}, true
	}
	return nil, false
}

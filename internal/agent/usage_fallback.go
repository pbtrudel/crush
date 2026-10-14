package agent

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"charm.land/fantasy"
)

func usageIsZero(usage fantasy.Usage) bool {
	return usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.ReasoningTokens == 0 &&
		usage.CacheCreationTokens == 0 &&
		usage.CacheReadTokens == 0
}

func fallbackStepUsage(messages []fantasy.Message, step fantasy.StepResult) (fantasy.Usage, bool) {
	if !usageIsZero(step.Usage) {
		return step.Usage, false
	}

	inputTokens := estimateMessageTokens(messages)
	outputTokens := estimateStepCompletionTokens(step)
	if inputTokens == 0 && outputTokens == 0 {
		return fantasy.Usage{}, false
	}

	return fantasy.Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}, true
}

func cloneFantasyMessages(messages []fantasy.Message) []fantasy.Message {
	cloned := make([]fantasy.Message, len(messages))
	for i, msg := range messages {
		cloned[i] = msg
		cloned[i].Content = append([]fantasy.MessagePart(nil), msg.Content...)
	}
	return cloned
}

func estimateMessageTokens(messages []fantasy.Message) int64 {
	var tokens int64
	for _, msg := range messages {
		tokens += approxTokenCount(string(msg.Role))
		for _, part := range msg.Content {
			tokens += estimateMessagePartTokens(part)
		}
	}
	return tokens
}

func estimateStepCompletionTokens(step fantasy.StepResult) int64 {
	var tokens int64
	for _, content := range step.Content {
		switch c := content.(type) {
		case fantasy.TextContent:
			tokens += approxTokenCount(c.Text)
		case *fantasy.TextContent:
			tokens += approxTokenCount(c.Text)
		case fantasy.ReasoningContent:
			tokens += approxTokenCount(c.Text)
		case *fantasy.ReasoningContent:
			tokens += approxTokenCount(c.Text)
		case fantasy.FileContent:
			tokens += estimateGeneratedFileTokens(c)
		case *fantasy.FileContent:
			tokens += estimateGeneratedFileTokens(*c)
		case fantasy.SourceContent:
			tokens += estimateSourceTokens(c)
		case *fantasy.SourceContent:
			tokens += estimateSourceTokens(*c)
		case fantasy.ToolCallContent:
			tokens += estimateToolCallTokens(c.ToolName, c.Input)
		case *fantasy.ToolCallContent:
			tokens += estimateToolCallTokens(c.ToolName, c.Input)
		case fantasy.ToolResultContent:
			if c.ProviderExecuted {
				tokens += estimateToolResultContentTokens(c.ToolCallID, c.ToolName, c.ClientMetadata, c.Result)
			}
		case *fantasy.ToolResultContent:
			if c.ProviderExecuted {
				tokens += estimateToolResultContentTokens(c.ToolCallID, c.ToolName, c.ClientMetadata, c.Result)
			}
		}
	}
	return tokens
}

func estimateMessagePartTokens(part fantasy.MessagePart) int64 {
	switch p := part.(type) {
	case fantasy.TextPart:
		return approxTokenCount(p.Text)
	case *fantasy.TextPart:
		return approxTokenCount(p.Text)
	case fantasy.ReasoningPart:
		return approxTokenCount(p.Text)
	case *fantasy.ReasoningPart:
		return approxTokenCount(p.Text)
	case fantasy.FilePart:
		return estimateFilePartTokens(p)
	case *fantasy.FilePart:
		return estimateFilePartTokens(*p)
	case fantasy.ToolCallPart:
		return estimateToolCallTokens(p.ToolName, p.Input)
	case *fantasy.ToolCallPart:
		return estimateToolCallTokens(p.ToolName, p.Input)
	case fantasy.ToolResultPart:
		return estimateToolResultContentTokens(p.ToolCallID, "", "", p.Output)
	case *fantasy.ToolResultPart:
		return estimateToolResultContentTokens(p.ToolCallID, "", "", p.Output)
	default:
		return 0
	}
}

func estimateToolCallTokens(toolName, input string) int64 {
	return approxTokenCount(toolName) + approxTokenCount(input)
}

func estimateToolResultContentTokens(toolCallID, toolName, metadata string, output fantasy.ToolResultOutputContent) int64 {
	tokens := approxTokenCount(toolCallID) + approxTokenCount(toolName) + approxTokenCount(metadata)
	switch result := output.(type) {
	case fantasy.ToolResultOutputContentText:
		tokens += approxTokenCount(result.Text)
	case *fantasy.ToolResultOutputContentText:
		tokens += approxTokenCount(result.Text)
	case fantasy.ToolResultOutputContentError:
		if result.Error != nil {
			tokens += approxTokenCount(result.Error.Error())
		}
	case *fantasy.ToolResultOutputContentError:
		if result.Error != nil {
			tokens += approxTokenCount(result.Error.Error())
		}
	case fantasy.ToolResultOutputContentMedia:
		tokens += estimateMediaTokens(result.MediaType, result.Text, []byte(result.Data))
	case *fantasy.ToolResultOutputContentMedia:
		tokens += estimateMediaTokens(result.MediaType, result.Text, []byte(result.Data))
	}
	return tokens
}

func estimateFilePartTokens(file fantasy.FilePart) int64 {
	return estimateMediaTokens(file.MediaType, file.Filename, file.Data)
}

func estimateGeneratedFileTokens(file fantasy.FileContent) int64 {
	return estimateMediaTokens(file.MediaType, "", file.Data)
}

const (
	// imageTokensPerPixel follows the common vision tiling model of
	// roughly one token per 750 pixels.
	imageTokensPerPixel = 750
	// minImageTokens and maxImageTokens bound a single image's estimate
	// so a tiny thumbnail still costs its fixed overhead and a huge
	// screenshot cannot dominate the budget.
	minImageTokens = 85
	maxImageTokens = 16000
)

// estimateMediaTokens estimates the tokens of a media payload. Images are
// costed from their pixel dimensions; payloads that do not decode as a
// known image format fall back to a conservative byte-size approximation.
func estimateMediaTokens(mediaType, text string, data []byte) int64 {
	tokens := approxTokenCount(mediaType) + approxTokenCount(text)
	if len(data) == 0 {
		return tokens
	}
	return tokens + estimateImageDataTokens(data)
}

func estimateImageDataTokens(data []byte) int64 {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// Not a decodable image: approximate the binary payload by size,
		// bounded so a corrupt large blob cannot blow up the estimate.
		return min(int64((len(data)+3)/4), int64(maxImageTokens))
	}
	tokens := int64(cfg.Width) * int64(cfg.Height) / imageTokensPerPixel
	return min(max(tokens, int64(minImageTokens)), int64(maxImageTokens))
}

func estimateSourceTokens(source fantasy.SourceContent) int64 {
	return approxTokenCount(string(source.SourceType)) +
		approxTokenCount(source.ID) +
		approxTokenCount(source.URL) +
		approxTokenCount(source.Title) +
		approxTokenCount(source.MediaType) +
		approxTokenCount(source.Filename)
}

func approxTokenCount(s string) int64 {
	if s == "" {
		return 0
	}
	return int64((len(s) + 3) / 4)
}

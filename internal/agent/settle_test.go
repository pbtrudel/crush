package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// The request must carry one answer per call. preparePrompt already picks
// the one result a call keeps when the transcript holds two (see
// TestPreparePrompt_DuplicateResultsKeepTheRealOne); toolResultsForCalls
// emits it once and consumes it, so a later assistant cannot repeat it.
func TestToolResultsForCalls_OneResultPerCall(t *testing.T) {
	t.Parallel()

	assistant := message.Message{
		ID:   "assistant",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
		},
	}
	byCall := map[string]fantasy.ToolResultPart{
		"call_1": {
			ToolCallID: "call_1",
			Output:     fantasy.ToolResultOutputContentText{Text: "ok"},
		},
	}

	got := toolResultsForCalls(assistant, byCall)

	require.Len(t, got.Content, 1, "one call gets exactly one answer")
	result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got.Content[0])
	require.True(t, ok)
	require.Equal(t, fantasy.ToolResultOutputContentText{Text: "ok"}, result.Output)
	require.Empty(t, byCall, "the emitted result is consumed")
}

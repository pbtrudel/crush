package agent

import (
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// A settle-on-read repair can answer a call that the run owning it answers
// again when the tool finally returns, leaving two stored results for one
// call. The request must still carry one answer per call, and the newest is
// the real one: a repair only settles calls with no result, so a duplicate
// can only be created afterwards.
func TestToolResultsForCalls_OneResultPerCall(t *testing.T) {
	t.Parallel()

	assistant := message.Message{
		ID:   "assistant",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
		},
	}
	// preparePrompt fills this in transcript order, so the last part stored
	// for a call is the newest.
	byCall := map[string][]fantasy.MessagePart{
		"call_1": {
			fantasy.ToolResultPart{
				ToolCallID: "call_1",
				Output:     fantasy.ToolResultOutputContentError{Error: errors.New(interruptedToolCallResult)},
			},
			fantasy.ToolResultPart{
				ToolCallID: "call_1",
				Output:     fantasy.ToolResultOutputContentText{Text: "ok"},
			},
		},
	}

	got := toolResultsForCalls(assistant, byCall)

	require.Len(t, got.Content, 1, "one call gets exactly one answer")
	result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got.Content[0])
	require.True(t, ok)
	require.Equal(t, fantasy.ToolResultOutputContentText{Text: "ok"}, result.Output,
		"the newest of the two results is the one sent")
}

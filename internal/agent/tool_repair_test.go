package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

// The repair contract, from fantasy v0.48.1:
//
// fantasy calls RepairToolCall from validateAndRepairToolCall (agent.go:1129)
// after validateToolCall fails: the call names a tool that is not available,
// its input is not valid JSON, or a required parameter is missing. It hands
// over ToolCallRepairOptions{OriginalToolCall, ValidationError,
// AvailableTools, SystemPrompt, Messages} (agent.go:119-125). A returned call
// is validated again and, if it passes, dispatched in place of the original.
// A nil result or an error keeps the original invalid call, which fantasy
// turns into a failed tool result. With no repair function wired, fantasy
// falls back to jsonrepair on the input only.
func TestRepairToolCallRenamesKnownAlias(t *testing.T) {
	t.Parallel()

	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "todo",
			Input:      `{"todos":[{"content":"a","status":"pending","active_form":"A"}]}`,
		},
		ValidationError: errors.New("tool not found: todo"),
		AvailableTools:  []fantasy.AgentTool{todosStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.NotNil(t, repaired)
	require.Equal(t, tools.TodosToolName, repaired.ToolName)
	require.Equal(t, "call-1", repaired.ToolCallID)
	require.Equal(t, opts.OriginalToolCall.Input, repaired.Input)
}

func TestRepairToolCallLeavesOtherNamesAlone(t *testing.T) {
	t.Parallel()

	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-2",
			ToolName:   "rm_rf",
			Input:      `{"path":"/"}`,
		},
		ValidationError: errors.New("tool not found: rm_rf"),
		AvailableTools:  []fantasy.AgentTool{todosStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.Nil(t, repaired)
}

func TestRepairToolCallSkipsAliasWhenTargetIsUnavailable(t *testing.T) {
	t.Parallel()

	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-3",
			ToolName:   "todo",
			Input:      `{"todos":[]}`,
		},
		ValidationError: errors.New("tool not found: todo"),
		AvailableTools:  []fantasy.AgentTool{viewStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.Nil(t, repaired)
}

func todosStubTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		tools.TodosToolName,
		"stub",
		func(context.Context, struct{}, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.ToolResponse{}, nil
		},
	)
}

func viewStubTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		tools.ViewToolName,
		"stub",
		func(context.Context, struct{}, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.ToolResponse{}, nil
		},
	)
}

// TestRepairToolCallRepairsBrokenJSONForExistingTool pins that wiring a repair
// function keeps fantasy's default jsonrepair pass for a call whose tool name
// is fine but whose input is not valid JSON.
func TestRepairToolCallRepairsBrokenJSONForExistingTool(t *testing.T) {
	t.Parallel()

	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-4",
			ToolName:   tools.TodosToolName,
			Input:      `{"a":1,}`,
		},
		ValidationError: errors.New("invalid JSON input"),
		AvailableTools:  []fantasy.AgentTool{todosStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.NotNil(t, repaired)
	require.Equal(t, tools.TodosToolName, repaired.ToolName)
	require.JSONEq(t, `{"a":1}`, repaired.Input)
}

// TestRepairToolCallRenamesAliasAndRepairsInput pins that an alias with broken
// JSON comes back both renamed and repaired.
func TestRepairToolCallRenamesAliasAndRepairsInput(t *testing.T) {
	t.Parallel()

	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-5",
			ToolName:   "todo",
			Input:      `{"todos":[{"content":"a"},]}`,
		},
		ValidationError: errors.New("tool not found: todo"),
		AvailableTools:  []fantasy.AgentTool{todosStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.NotNil(t, repaired)
	require.Equal(t, tools.TodosToolName, repaired.ToolName)
	require.JSONEq(t, `{"todos":[{"content":"a"}]}`, repaired.Input)
}

// TestRepairToolCallReturnsNilWhenNothingChanges pins that an untouched call
// leaves fantasy's original error in place.
func TestRepairToolCallReturnsNilWhenNothingChanges(t *testing.T) {
	t.Parallel()

	original := fantasy.ToolCallContent{
		ToolCallID: "call-6",
		ToolName:   "rm_rf",
		Input:      `{"path":"/"}`,
	}
	opts := fantasy.ToolCallRepairOptions{
		OriginalToolCall: original,
		ValidationError:  errors.New("tool not found: rm_rf"),
		AvailableTools:   []fantasy.AgentTool{todosStubTool()},
	}

	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.Nil(t, repaired)
}

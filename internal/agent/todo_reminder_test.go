package agent

import (
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// todoTestStep builds a step result from tool calls and their results, the
// shape OnStepFinish receives after a model step.
func todoTestStep(calls []fantasy.ToolCallContent, results []fantasy.ToolResultContent) fantasy.StepResult {
	content := make(fantasy.ResponseContent, 0, len(calls)+len(results))
	for _, call := range calls {
		content = append(content, call)
	}
	for _, result := range results {
		content = append(content, result)
	}
	return fantasy.StepResult{Response: fantasy.Response{Content: content}}
}

func todoTestCall(id, name string) fantasy.ToolCallContent {
	return fantasy.ToolCallContent{ToolCallID: id, ToolName: name}
}

func todoTestOK(id, name string) fantasy.ToolResultContent {
	return fantasy.ToolResultContent{
		ToolCallID: id,
		ToolName:   name,
		Result:     fantasy.ToolResultOutputContentText{Text: "ok"},
	}
}

func todoTestErr(id, name string) fantasy.ToolResultContent {
	return fantasy.ToolResultContent{
		ToolCallID: id,
		ToolName:   name,
		Result:     fantasy.ToolResultOutputContentError{Error: errors.New("failed")},
	}
}

func TestStepMadeTaskProgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step fantasy.StepResult
		want bool
	}{
		{
			name: "successful edit",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.EditToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.EditToolName)},
			),
			want: true,
		},
		{
			name: "successful multiedit",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.MultiEditToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.MultiEditToolName)},
			),
			want: true,
		},
		{
			name: "successful write",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.WriteToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.WriteToolName)},
			),
			want: true,
		},
		{
			name: "successful bash",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.BashToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.BashToolName)},
			),
			want: true,
		},
		{
			name: "successful lsp replace symbol",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.ReplaceSymbolToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.ReplaceSymbolToolName)},
			),
			want: true,
		},
		{
			name: "successful lsp rename",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.RenameToolName)},
				[]fantasy.ToolResultContent{todoTestOK("1", tools.RenameToolName)},
			),
			want: true,
		},
		{
			name: "read-only tools",
			step: todoTestStep(
				[]fantasy.ToolCallContent{
					todoTestCall("1", tools.ViewToolName),
					todoTestCall("2", tools.GrepToolName),
				},
				[]fantasy.ToolResultContent{
					todoTestOK("1", tools.ViewToolName),
					todoTestOK("2", tools.GrepToolName),
				},
			),
			want: false,
		},
		{
			name: "todo list already updated",
			step: todoTestStep(
				[]fantasy.ToolCallContent{
					todoTestCall("1", tools.EditToolName),
					todoTestCall("2", tools.TodosToolName),
				},
				[]fantasy.ToolResultContent{
					todoTestOK("1", tools.EditToolName),
					todoTestOK("2", tools.TodosToolName),
				},
			),
			want: false,
		},
		{
			name: "failed edit",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.EditToolName)},
				[]fantasy.ToolResultContent{todoTestErr("1", tools.EditToolName)},
			),
			want: false,
		},
		{
			name: "missing result",
			step: todoTestStep(
				[]fantasy.ToolCallContent{todoTestCall("1", tools.EditToolName)},
				nil,
			),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, stepMadeTaskProgress(tt.step))
		})
	}
}

func TestTodoReminder(t *testing.T) {
	t.Parallel()

	t.Run("in progress task is named", func(t *testing.T) {
		t.Parallel()
		got := todoReminder([]session.Todo{
			{Content: "Read the code", Status: session.TodoStatusCompleted},
			{Content: "Run the tests", Status: session.TodoStatusInProgress},
			{Content: "Update the docs", Status: session.TodoStatusPending},
		})
		require.Contains(t, got, "Run the tests")
		require.Contains(t, got, "in_progress")
		require.Contains(t, got, "completed")
		require.Contains(t, got, "todos")
		require.NotContains(t, got, "Read the code")
	})

	t.Run("no task in progress", func(t *testing.T) {
		t.Parallel()
		got := todoReminder([]session.Todo{
			{Content: "Read the code", Status: session.TodoStatusPending},
			{Content: "Update the docs", Status: session.TodoStatusPending},
		})
		require.Contains(t, got, "2 open")
		require.Contains(t, got, "completed")
		require.Contains(t, got, "todos")
	})

	t.Run("all tasks completed", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, todoReminder([]session.Todo{
			{Content: "Read the code", Status: session.TodoStatusCompleted},
		}))
	})
}

func TestTodoReminderDue(t *testing.T) {
	t.Parallel()

	progress := todoTestStep(
		[]fantasy.ToolCallContent{todoTestCall("1", tools.EditToolName)},
		[]fantasy.ToolResultContent{todoTestOK("1", tools.EditToolName)},
	)
	openTodos := []session.Todo{{Content: "Run the tests", Status: session.TodoStatusInProgress}}

	tests := []struct {
		name           string
		step           fantasy.StepResult
		todos          []session.Todo
		stepsSinceLast int
		sent           int
		want           bool
	}{
		{
			name:           "due after progress with open todos",
			step:           progress,
			todos:          openTodos,
			stepsSinceLast: todoReminderMinGap,
			want:           true,
		},
		{
			name:           "not due without open todos",
			step:           progress,
			todos:          []session.Todo{{Content: "Run the tests", Status: session.TodoStatusCompleted}},
			stepsSinceLast: todoReminderMinGap,
			want:           false,
		},
		{
			name:           "not due without task progress",
			step:           todoTestStep(nil, nil),
			todos:          openTodos,
			stepsSinceLast: todoReminderMinGap,
			want:           false,
		},
		{
			name:           "not due before the gap elapses",
			step:           progress,
			todos:          openTodos,
			stepsSinceLast: todoReminderMinGap - 1,
			want:           false,
		},
		{
			name:           "not due after the limit",
			step:           progress,
			todos:          openTodos,
			stepsSinceLast: todoReminderMinGap,
			sent:           todoReminderLimit,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, todoReminderDue(tt.step, tt.todos, tt.stepsSinceLast, tt.sent))
		})
	}
}

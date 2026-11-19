package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
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
		}, 0)
		require.Contains(t, got, "Run the tests")
		require.Contains(t, got, "unfinished")
		require.Contains(t, got, "completed")
		require.Contains(t, got, "todos")
		require.NotContains(t, got, "Read the code")
	})

	t.Run("no task in progress names the first open item", func(t *testing.T) {
		t.Parallel()
		got := todoReminder([]session.Todo{
			{Content: "Read the code", Status: session.TodoStatusPending},
			{Content: "Update the docs", Status: session.TodoStatusPending},
		}, 0)
		require.Contains(t, got, "Read the code")
		require.Contains(t, got, "completed")
		require.Contains(t, got, "todos")
	})

	t.Run("all tasks completed", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, todoReminder([]session.Todo{
			{Content: "Read the code", Status: session.TodoStatusCompleted},
		}, 0))
	})

	t.Run("escalates once reminders go unanswered", func(t *testing.T) {
		t.Parallel()
		todos := []session.Todo{{Content: "Deploy it", Status: session.TodoStatusInProgress}}

		first := todoReminder(todos, 0)
		require.Contains(t, first, "If you finished that task in the previous step")

		escalated := todoReminder(todos, todoReminderEscalateAfter)
		require.Contains(t, escalated, "Deploy it")
		require.Contains(t, escalated, "went unanswered")
		require.Contains(t, escalated, "Do not end this turn claiming that work is complete")
		require.NotContains(t, escalated, "If you finished that task in the previous step")
	})
}

func TestStaleTodo(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Run the tests", staleTodo([]session.Todo{
		{Content: "Read the code", Status: session.TodoStatusCompleted},
		{Content: "Update the docs", Status: session.TodoStatusPending},
		{Content: "Run the tests", Status: session.TodoStatusInProgress},
	}))
	require.Equal(t, "Read the code", staleTodo([]session.Todo{
		{Content: "Read the code", Status: session.TodoStatusPending},
	}))
	require.Empty(t, staleTodo([]session.Todo{
		{Content: "Read the code", Status: session.TodoStatusCompleted},
	}))
	require.Empty(t, staleTodo(nil))
}

func TestTodoStillOpen(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{
		{Content: "Run the tests", Status: session.TodoStatusCompleted},
		{Content: "Deploy it", Status: session.TodoStatusInProgress},
	}
	require.False(t, todoStillOpen(todos, "Run the tests"))
	require.True(t, todoStillOpen(todos, "Deploy it"))
	// An item dropped from the list counts as resolved.
	require.False(t, todoStillOpen(todos, "Never existed"))
}

func TestStaleTodoAtTurnEnd(t *testing.T) {
	t.Parallel()

	open := []session.Todo{{Content: "Deploy it", Status: session.TodoStatusInProgress}}
	done := []session.Todo{{Content: "Deploy it", Status: session.TodoStatusCompleted}}

	// One reminder is still a nudge; the note starts once it goes unanswered.
	require.False(t, staleTodoAtTurnEnd("Deploy it", 0, open))
	require.True(t, staleTodoAtTurnEnd("Deploy it", todoReminderEscalateAfter-1, open))
	require.False(t, staleTodoAtTurnEnd("Deploy it", todoReminderEscalateAfter, done))
	require.False(t, staleTodoAtTurnEnd("", todoReminderEscalateAfter, open))
}

func TestStaleTodoNote(t *testing.T) {
	t.Parallel()

	got := staleTodoNote("Deploy it", 3)
	require.Contains(t, got, "Deploy it")
	require.Contains(t, got, "3 reminders")
	require.Contains(t, got, "Crush added this note")
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

// slowCompactionModel answers from a script like scriptedModel and stalls
// the compaction request, so the compaction outlasts the turn's cleanup
// context.
type slowCompactionModel struct {
	scriptedModel
	delay time.Duration
}

func (m *slowCompactionModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if isCompactionCall(call) {
		time.Sleep(m.delay)
	}
	return m.scriptedModel.Stream(ctx, call)
}

// isCompactionCall reports whether the request carries the compaction
// instruction, the only call the note test stalls.
func isCompactionCall(call fantasy.Call) bool {
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			text, ok := part.(fantasy.TextPart)
			if ok && strings.Contains(text.Text, "Compact the conversation above") {
				return true
			}
		}
	}
	return false
}

// finalAssistantText returns the text of the last assistant message that is
// not a compaction summary.
func finalAssistantText(t *testing.T, env fakeEnv, sessionID string) string {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	text := ""
	for _, msg := range msgs {
		if msg.Role != message.Assistant || msg.IsSummaryMessage {
			continue
		}
		text = msg.Content().Text
	}
	return text
}

// The note is written after the end-of-turn compaction, so it cannot borrow
// the turn's cleanup context: a compaction that outlasts that context would
// expire it and the note would be lost.
func TestStaleTodoNoteSurvivesASlowCompaction(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the cleanup context's 5 s budget")
	}
	env := testEnv(t)
	// Five progress steps leave three unanswered reminders about the same
	// item; the sixth answer closes the turn at 150k of a 200k window,
	// above the 100k threshold, so the turn compacts afterwards.
	script := make([]scriptedCall, 0, 7)
	for range 5 {
		script = append(script, callTool(tools.BashToolName, 150_000))
	}
	script = append(script,
		answer("done", fantasy.FinishReasonStop, 150_000),
		answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000),
	)
	model := &slowCompactionModel{
		scriptedModel: scriptedModel{script: script},
		// Comfortably longer than the 5 s the turn's cleanup context
		// allows; that budget is a literal in Run, so the test cannot
		// lower it.
		delay: 6 * time.Second,
	}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: tools.BashToolName})
	sess, err := env.sessions.Create(t.Context(), "slow compaction")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(t.Context(), sess.ID, []session.Todo{
		{Content: "Deploy it", Status: session.TodoStatusInProgress},
	}))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, err = agent.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Contains(t, finalAssistantText(t, env, sess.ID), `Todo list still shows "Deploy it" as unfinished`)
}

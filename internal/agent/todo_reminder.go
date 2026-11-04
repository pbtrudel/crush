package agent

import (
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/session"
)

const (
	// todoReminderMinGap is the minimum number of model steps between todo
	// reminders in one turn, so a long task is not nagged on every step.
	todoReminderMinGap = 2
	// todoReminderLimit caps the reminders one turn can inject. A model
	// that ignores every reminder must not be nagged indefinitely.
	todoReminderLimit = 6
)

// taskProgressToolNames are the tools whose use can finish a task. A step
// that runs one of them without touching the todo list is the moment the
// model is most likely to have completed work it should report.
var taskProgressToolNames = map[string]struct{}{
	tools.EditToolName:          {},
	tools.MultiEditToolName:     {},
	tools.WriteToolName:         {},
	tools.BashToolName:          {},
	tools.ReplaceSymbolToolName: {},
	tools.RenameToolName:        {},
}

// stepMadeTaskProgress reports whether the step ran a tool that can finish a
// task and did not update the todo list itself. Failed calls do not count:
// they cannot have completed anything.
func stepMadeTaskProgress(step fantasy.StepResult) bool {
	succeeded := make(map[string]struct{})
	for _, result := range step.Content.ToolResults() {
		if _, failed := result.Result.(fantasy.ToolResultOutputContentError); failed {
			continue
		}
		succeeded[result.ToolCallID] = struct{}{}
	}

	progress := false
	for _, call := range step.Content.ToolCalls() {
		if call.ToolName == tools.TodosToolName {
			return false
		}
		if _, ok := taskProgressToolNames[call.ToolName]; !ok {
			continue
		}
		if _, ok := succeeded[call.ToolCallID]; !ok {
			continue
		}
		progress = true
	}
	return progress
}

// todoReminderDue reports whether a step warrants a todo-list reminder. The
// gap and the limit keep a model that ignores the reminder from being nagged
// on every step of a long turn.
func todoReminderDue(step fantasy.StepResult, todos []session.Todo, stepsSinceLast, sent int) bool {
	return sent < todoReminderLimit &&
		stepsSinceLast >= todoReminderMinGap &&
		session.HasIncompleteTodos(todos) &&
		stepMadeTaskProgress(step)
}

// todoReminder renders the advisory message injected after a step that made
// progress while the todo list still has open tasks. It is a user-role
// system reminder so every provider accepts it.
func todoReminder(todos []session.Todo) string {
	var open int
	var inProgress string
	for _, todo := range todos {
		if todo.Status == session.TodoStatusCompleted {
			continue
		}
		open++
		if todo.Status == session.TodoStatusInProgress && inProgress == "" {
			inProgress = todo.Content
		}
	}
	if open == 0 {
		return ""
	}

	var detail string
	if inProgress != "" {
		detail = fmt.Sprintf("Your todo list still shows %q as in_progress.", inProgress)
	} else {
		detail = fmt.Sprintf("Your todo list has %d open task(s) and none in progress.", open)
	}

	return fmt.Sprintf(
		"<system_reminder>%s If you finished a task in the previous step, update the todo list now: mark the finished task completed and the next task in_progress in the same `todos` call. Do not mention this reminder to the user.</system_reminder>",
		detail,
	)
}

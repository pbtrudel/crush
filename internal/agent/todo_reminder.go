package agent

import (
	"encoding/json"
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
	// todoReminderEscalateAfter is how many reminders about the same
	// item may go unanswered before Crush stops asking and starts
	// insisting, and before it writes the turn-end note.
	todoReminderEscalateAfter = 2
)

// taskProgressToolNames are the tools whose use can finish a task. A step
// that runs one of them without touching the todo list is the moment the
// model is most likely to have completed work it should report.
//
// Reading a file, waiting for a teammate or reading a background job is not
// in the map: those steps cannot finish an item, so an accurate list would
// only be nagged. Bash is judged per command instead, see bashMadeProgress.
var taskProgressToolNames = map[string]struct{}{
	tools.EditToolName:          {},
	tools.MultiEditToolName:     {},
	tools.WriteToolName:         {},
	tools.ReplaceSymbolToolName: {},
	tools.RenameToolName:        {},
	// Delegation and team-task transitions can finish a task without the
	// Lead touching a file, so they must trigger the same reminder.
	AgentToolName:          {},
	AgentDispatchToolName:  {},
	AgentSendToolName:      {},
	AgentStopToolName:      {},
	SpawnTeammateToolName:  {},
	TeamTaskUpdateToolName: {},
}

// bashMadeProgress reports whether a bash call could have finished a task.
// A command that is clearly read-only, or that only waits, cannot. A command
// the classifier does not recognize counts as progress: the reminder is
// worth one after a build or a test run, and guessing the other way would
// let completed work go unreported. A call the model put in the background
// counts too, whatever its command: it may be doing the work that finishes
// the item, and the reminder is cheap next to missing that.
func bashMadeProgress(input string) bool {
	var params tools.BashParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return true
	}
	return params.RunInBackground || !tools.IsReadOnlyCommand(params.Command)
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
		if _, ok := succeeded[call.ToolCallID]; !ok {
			continue
		}
		if call.ToolName == tools.BashToolName {
			if bashMadeProgress(call.Input) {
				progress = true
			}
			continue
		}
		if _, ok := taskProgressToolNames[call.ToolName]; !ok {
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

// staleTodo returns the item the todo list most disagrees with: the first
// in_progress item, else the first still-open one. It returns "" when the
// list is complete or empty.
func staleTodo(todos []session.Todo) string {
	open := ""
	for _, todo := range todos {
		if todo.Status == session.TodoStatusCompleted {
			continue
		}
		if todo.Status == session.TodoStatusInProgress {
			return todo.Content
		}
		if open == "" {
			open = todo.Content
		}
	}
	return open
}

// todoStillOpen reports whether the named item is still not completed. An
// item the model dropped from the list counts as resolved.
func todoStillOpen(todos []session.Todo, content string) bool {
	for _, todo := range todos {
		if todo.Content == content {
			return todo.Status != session.TodoStatusCompleted
		}
	}
	return false
}

// todoReminder renders the advisory message injected after a step that made
// progress while the todo list still has open tasks. It is a user-role
// system reminder so every provider accepts it. Once the model has ignored
// reminders about the same item, it asks the model to verify the item rather
// than to update it: a list that is right is better left alone, and a final
// answer that reports the work as done while the list says otherwise is the
// failure this guard exists to prevent.
func todoReminder(todos []session.Todo, unanswered int) string {
	stale := staleTodo(todos)
	if stale == "" {
		return ""
	}

	if unanswered >= todoReminderEscalateAfter {
		return fmt.Sprintf(
			"<system_reminder>Your todo list still shows %q as unfinished, and %d reminders about it went unanswered. Check whether that item is actually done. If it is done, update the list now. If it is not done, leave the entry as it is and continue the work; do not mark it completed just to close this reminder. Do not mention this reminder to the user.</system_reminder>",
			stale,
			unanswered,
		)
	}

	return fmt.Sprintf(
		"<system_reminder>Your todo list still shows %q as unfinished. If you finished that task in the previous step, update the todo list now: mark the finished task completed and the next task in_progress in the same `todos` call. Do not mention this reminder to the user.</system_reminder>",
		stale,
	)
}

// staleTodoAtTurnEnd reports whether a turn that is finishing should carry
// the stale list note: the same item stayed open through repeated reminders.
func staleTodoAtTurnEnd(item string, unanswered int, todos []session.Todo) bool {
	return item != "" &&
		unanswered+1 >= todoReminderEscalateAfter &&
		todoStillOpen(todos, item)
}

// staleTodoNote is appended to the model's final message when a turn ends
// with the todo list still behind the work and the model ignored reminders
// about the same item. The model has had its chance to report this, so the
// note does not rely on it noticing again; it keeps the user from
// discovering a stale list later.
func staleTodoNote(item string, reminders int) string {
	return fmt.Sprintf(
		"\n\n---\nTodo list still shows %q as unfinished after %d reminders. Crush added this note because the list was not updated for this turn.",
		item,
		reminders,
	)
}

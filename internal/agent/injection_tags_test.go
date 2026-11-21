package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

// The report names each hidden row the agent writes by the tag its text
// opens with (logreport.InjectionTag). Rewording a reminder or a notice
// must keep the tag, or the report would count it as a hidden turn.
func TestInjectedRowsCarryTheReportTags(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{{Content: "write the test", Status: session.TodoStatusInProgress}}
	require.Equal(t, "system_reminder", logreport.InjectionTag(todoReminder(todos, 0)))
	require.Equal(t, "system_reminder", logreport.InjectionTag(todoReminder(todos, todoReminderEscalateAfter)))
	require.Equal(t, "system_reminder", logreport.InjectionTag(repeatToolReminder("bash", 3)))
	require.Equal(t, "background_job_notice", logreport.InjectionTag(backgroundJobNotice(shell.BackgroundJobEvent{ShellID: "001", Command: "make", ExitCode: 2})))
	require.Equal(t, "turn_end_gate", logreport.InjectionTag(gateMessage([]gateReason{{label: "check: completion", text: "check it"}})))
}

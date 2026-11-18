package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestShellCountsForegroundCommands(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "b2", Name: "bash"},
		message.ToolCall{ID: "b3", Name: "bash"},
		message.ToolCall{ID: "b4", Name: "bash"},
		message.ToolCall{ID: "b5", Name: "bash"},
		message.ToolCall{ID: "j1", Name: "job_output"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "b2", Name: "bash", Metadata: `{"exit_code":2}`})
	log.result("s1", message.ToolResult{ToolCallID: "b3", Name: "bash", Metadata: `{"interrupted":true}`})
	// A run_in_background command that finished in the probe window
	// reports its exit inline and is a foreground command.
	log.result("s1", message.ToolResult{ToolCallID: "b4", Name: "bash", Metadata: `{"background":true,"shell_id":"x","exit_code":1}`})
	// Moved to the background by a build that stamps: not counted.
	log.result("s1", message.ToolResult{ToolCallID: "b5", Name: "bash", Metadata: `{"background":true,"shell_id":"y"}`})
	// Only shell results count, whatever their metadata holds.
	log.result("s1", message.ToolResult{ToolCallID: "j1", Name: "job_output", Metadata: `{"exit_code":3}`})

	shell := log.report(Options{}).Shell
	require.Equal(t, Shell{Commands: 4, NonZero: 2, Interrupted: 1, FailureRate: 0.75}, shell)
}

func TestShellResultsFromBuildsThatDoNotStampAreUnstamped(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	// The exit code is in the text only: the report does not read it.
	log.step("s1", oldBuild, message.ToolCall{ID: "o1", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o1", Name: "bash", Content: "boom\nExit code 1\n\n<cwd>/x</cwd>"})
	log.step("s1", "", message.ToolCall{ID: "o2", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o2", Name: "bash", Content: "ok"})
	// Metadata that is not a JSON object carries no stamp.
	log.step("s1", oldBuild, message.ToolCall{ID: "o3", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "o3", Name: "bash", Metadata: "plain text"})

	require.Equal(t, Shell{Unstamped: 3}, log.report(Options{}).Shell)
}

func TestShellResultsWithAnErrorKindAreNotUnstamped(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	// A crash repair and a refusal add an error kind to the result, on a
	// step with no build stamp: neither a command nor an old build's
	// bare result.
	log.step("s1", "", message.ToolCall{ID: "e1", Name: "bash"})
	log.result("s1", message.ToolResult{ToolCallID: "e1", Name: "bash", Metadata: `{"error_kind":"interrupted"}`, IsError: true})

	require.Equal(t, Shell{}, log.report(Options{}).Shell)
}

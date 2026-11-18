package logreport

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// Builds used by the tests: newBuild stamps its tool results, oldBuild
// was built before the stamps existed.
const (
	newBuild = "0.100.0-fork.120"
	oldBuild = "0.100.0-fork.90"
)

func TestToolsCountsCallsErrorsAndKinds(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "c1", Name: "edit"},
		message.ToolCall{ID: "c2", Name: "edit"},
		message.ToolCall{ID: "c3", Name: "view"},
		message.ToolCall{ID: "c4", Name: "read"},
		message.ToolCall{ID: "c5", Name: "fetch"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "c1", Name: "edit", IsError: true, Metadata: `{"error_kind":"not_read"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c2", Name: "edit"})
	log.result("s1", message.ToolResult{ToolCallID: "c3", Name: "view", IsError: true, Metadata: `{"error_kind":"a_kind_from_a_later_build"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c4", Name: "read", IsError: true})
	// fetch's call has no result: it counts as a call without an error.

	tools := log.report(Options{}).Tools
	require.Equal(t, 5, tools.Calls)
	require.Equal(t, 3, tools.Errors)
	require.InDelta(t, 0.6, tools.Rate, 1e-9)
	require.Equal(t, []string{"edit", "fetch", "read", "view"}, toolNames(tools.ByTool), "most calls first, ties by name")
	edit := tools.ByTool[0]
	require.Equal(t, 2, edit.Calls)
	require.Equal(t, 1, edit.Errors)
	require.Equal(t, map[string]int{"not_read": 1}, edit.Kinds)
	require.Equal(t, "view+read", tools.ViewRead.Name)
	require.Equal(t, 2, tools.ViewRead.Calls)
	require.Equal(t, 2, tools.ViewRead.Errors)
	// An unknown kind is listed under its own name. The read error has
	// no kind and its build stamps kinds, so it is "other".
	require.Equal(t, map[string]int{"a_kind_from_a_later_build": 1, "other": 1}, tools.ViewRead.Kinds)
}

func TestErrorsWithoutKindFromBuildsThatDoNotStampAreUnstamped(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("new", "title")
	log.session("old", "title")
	// The new build stamps its shell results, so its errors without a
	// kind are "other".
	log.step("new", newBuild, message.ToolCall{ID: "n1", Name: "bash"}, message.ToolCall{ID: "n2", Name: "glob"})
	log.result("new", message.ToolResult{ToolCallID: "n1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("new", message.ToolResult{ToolCallID: "n2", Name: "glob", IsError: true})
	// The old build stamps nothing; a step without a build is unknown.
	log.step("old", oldBuild, message.ToolCall{ID: "o1", Name: "glob"}, message.ToolCall{ID: "o2", Name: "bash"})
	log.result("old", message.ToolResult{ToolCallID: "o1", Name: "glob", IsError: true})
	log.result("old", message.ToolResult{ToolCallID: "o2", Name: "bash", Content: "ok"})
	log.step("old", "", message.ToolCall{ID: "o3", Name: "glob"})
	log.result("old", message.ToolResult{ToolCallID: "o3", Name: "glob", IsError: true})
	// A result whose call is not in the window has no known build.
	log.result("old", message.ToolResult{ToolCallID: "gone", Name: "glob", IsError: true})

	glob := log.report(Options{}).Tools.ByTool[0]
	require.Equal(t, "glob", glob.Name)
	require.Equal(t, map[string]int{"other": 1, "unstamped": 3}, glob.Kinds)
}

func TestStampsOnOtherToolsDoNotMarkABuild(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	// Only a shell result's exit code marks its build; any tool's
	// metadata can hold an exit_code key.
	log.step("s1", oldBuild, message.ToolCall{ID: "m1", Name: "mcp_tool"}, message.ToolCall{ID: "g1", Name: "glob"})
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "g1", Name: "glob", IsError: true})

	require.Equal(t, map[string]int{"unstamped": 1}, log.report(Options{}).Tools.ByTool[0].Kinds)
}

func TestToolsReadsSameSecondRowsAndOddMetadata(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "m1", Name: "mcp_tool"},
		message.ToolCall{ID: "m2", Name: "mcp_tool"},
	)
	// created_at has one-second resolution: a result written in the same
	// second as its call follows it by rowid, and still finds its build.
	log.at = log.at.Add(-time.Second)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	// Metadata that is not a JSON object carries no kind.
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", IsError: true, Metadata: "plain text"})
	log.result("s1", message.ToolResult{ToolCallID: "m2", Name: "mcp_tool", IsError: true, Metadata: `["error_kind"]`})

	tools := log.report(Options{}).Tools
	require.Equal(t, "mcp_tool", tools.ByTool[0].Name)
	require.Equal(t, map[string]int{"other": 2}, tools.ByTool[0].Kinds)
}

func toolNames(stats []ToolStats) []string {
	names := make([]string, len(stats))
	for i, s := range stats {
		names[i] = s.Name
	}
	return names
}

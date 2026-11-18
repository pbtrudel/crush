package logreport

import (
	"bytes"
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
		message.ToolCall{ID: "c5", Name: "read"},
		message.ToolCall{ID: "c6", Name: "fetch"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "c1", Name: "edit", IsError: true, Metadata: `{"error_kind":"not_read"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c2", Name: "edit"})
	log.result("s1", message.ToolResult{ToolCallID: "c3", Name: "view", IsError: true, Metadata: `{"error_kind":"a_kind_from_a_later_build"}`})
	log.result("s1", message.ToolResult{ToolCallID: "c4", Name: "read", IsError: true})
	log.result("s1", message.ToolResult{ToolCallID: "c5", Name: "read"})
	// fetch's call has no result, so nothing shows it ran under that
	// name: it counts as a call without an error, under "unrecognized".

	tools := log.report(Options{}).Tools
	require.Equal(t, 6, tools.Calls)
	require.Equal(t, 3, tools.Errors)
	require.InDelta(t, 0.5, tools.Rate, 1e-9)
	require.Equal(t, []string{"edit", "read", unrecognizedTool, "view"}, toolNames(tools.ByTool), "most calls first, ties by name")
	edit := tools.ByTool[0]
	require.Equal(t, 2, edit.Calls)
	require.Equal(t, 1, edit.Errors)
	require.Equal(t, map[string]int{"not_read": 1}, edit.Kinds)
	require.Equal(t, "view+read", tools.ViewRead.Name)
	require.Equal(t, 3, tools.ViewRead.Calls)
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
	log.step("new", newBuild, message.ToolCall{ID: "n1", Name: "bash"}, message.ToolCall{ID: "n2", Name: "glob"}, message.ToolCall{ID: "n3", Name: "glob"})
	log.result("new", message.ToolResult{ToolCallID: "n1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("new", message.ToolResult{ToolCallID: "n2", Name: "glob", IsError: true})
	// A result that is not an error shows glob ran under its name.
	log.result("new", message.ToolResult{ToolCallID: "n3", Name: "glob"})
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
	log.step("s1", oldBuild, message.ToolCall{ID: "m1", Name: "mcp_tool"}, message.ToolCall{ID: "g1", Name: "glob"}, message.ToolCall{ID: "g2", Name: "glob"})
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "g1", Name: "glob", IsError: true})
	log.result("s1", message.ToolResult{ToolCallID: "g2", Name: "glob"})

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
		message.ToolCall{ID: "m3", Name: "mcp_tool"},
	)
	// created_at has one-second resolution: a result written in the same
	// second as its call follows it by rowid, and still finds its build.
	log.at = log.at.Add(-time.Second)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	// Metadata that is not a JSON object carries no kind.
	log.result("s1", message.ToolResult{ToolCallID: "m1", Name: "mcp_tool", IsError: true, Metadata: "plain text"})
	log.result("s1", message.ToolResult{ToolCallID: "m2", Name: "mcp_tool", IsError: true, Metadata: `["error_kind"]`})
	log.result("s1", message.ToolResult{ToolCallID: "m3", Name: "mcp_tool"})

	tools := log.report(Options{}).Tools
	require.Equal(t, "mcp_tool", tools.ByTool[0].Name)
	require.Equal(t, map[string]int{"other": 2}, tools.ByTool[0].Kinds)
}

func TestInventedToolNamesAreNotPrinted(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	invented := "invented_" + marker
	log.session("s1", "title")
	log.step("s1", newBuild,
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "i1", Name: invented},
		message.ToolCall{ID: "i2", Name: invented},
		message.ToolCall{ID: "g1", Name: "glob"},
	)
	log.result("s1", message.ToolResult{ToolCallID: "b1", Name: "bash", Metadata: `{"exit_code":0}`})
	log.result("s1", message.ToolResult{ToolCallID: "i1", Name: invented, IsError: true, Content: "no such tool", Metadata: `{"error_kind":"unknown_tool"}`})
	log.result("s1", message.ToolResult{ToolCallID: "i2", Name: invented, IsError: true, Content: "no such tool"})
	// A real name whose only result carries a kind is recognized.
	log.result("s1", message.ToolResult{ToolCallID: "g1", Name: "glob", IsError: true, Metadata: `{"error_kind":"invalid_arguments"}`})

	r := log.report(Options{})
	tools := r.Tools
	require.Equal(t, 4, tools.Calls)
	require.Equal(t, 3, tools.Errors)
	require.Equal(t, []string{unrecognizedTool, "bash", "glob"}, toolNames(tools.ByTool), "most calls first, ties by name")
	byName := map[string]ToolStats{}
	for _, s := range tools.ByTool {
		byName[s.Name] = s
	}
	require.Equal(t, 2, byName[unrecognizedTool].Calls)
	require.Equal(t, 2, byName[unrecognizedTool].Errors)
	require.InDelta(t, 1.0, byName[unrecognizedTool].Rate, 1e-9)
	// The stamped failure keeps its kind; the failure without metadata
	// reads "other" because the build that made the call stamps kinds.
	require.Equal(t, map[string]int{"unknown_tool": 1, "other": 1}, byName[unrecognizedTool].Kinds)
	require.Equal(t, 1, byName["bash"].Calls)
	require.Zero(t, byName["bash"].Errors)
	require.Equal(t, 1, byName["glob"].Calls)
	require.Equal(t, map[string]int{"invalid_arguments": 1}, byName["glob"].Kinds)

	var text, js bytes.Buffer
	require.NoError(t, r.WriteText(&text))
	require.NoError(t, r.WriteJSON(&js))
	require.Contains(t, text.String(), unrecognizedTool)
	require.NotContains(t, text.String(), marker)
	require.NotContains(t, js.String(), marker)
}

func toolNames(stats []ToolStats) []string {
	names := make([]string, len(stats))
	for i, s := range stats {
		names[i] = s.Name
	}
	return names
}

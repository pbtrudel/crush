package logreport

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestByModelAndByBuild(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	log.child("team-x", "lead")
	prompt := func(text string) {
		log.add("lead", message.User, message.TextContent{Text: text})
	}
	result := func(session, id, name string, failed bool, metadata string) {
		log.result(session, message.ToolResult{ToolCallID: id, Name: name, IsError: failed, Metadata: metadata})
	}

	prompt("one")
	log.step("lead", newBuild,
		message.ToolCall{ID: "e1", Name: "edit"},
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "g1", Name: "glob"},
	)
	result("lead", "e1", "edit", true, `{"error_kind":"text_not_found"}`)
	result("lead", "b1", "bash", false, `{"exit_code":1}`)
	result("lead", "g1", "glob", false, "")
	// An interrupted command carries no exit code and is a foreground
	// command all the same, and a failure.
	log.lastStep("lead", newBuild, message.TurnEndCompleted, message.ToolCall{ID: "b6", Name: "bash"})
	result("lead", "b6", "bash", true, `{"interrupted":true}`)
	// A child session's calls count for its model, not its prompts.
	log.step("team-x", newBuild, message.ToolCall{ID: "b2", Name: "bash"})
	result("team-x", "b2", "bash", false, `{"exit_code":0}`)

	log.model, log.provider = "model-b", "provider-b"
	prompt("two")
	log.step("lead", oldBuild,
		message.ToolCall{ID: "b3", Name: "bash"},
		message.ToolCall{ID: "e2", Name: "edit"},
		message.ToolCall{ID: "m1", Name: "multiedit"},
		message.ToolCall{ID: "u1", Name: "made_up_tool"},
	)
	result("lead", "b3", "bash", false, "")
	result("lead", "e2", "edit", false, "")
	result("lead", "m1", "multiedit", true, "")
	result("lead", "u1", "made_up_tool", true, `{"error_kind":"unknown_tool"}`)
	// A result whose call is not in the window is attributed to no one.
	result("lead", "gone", "edit", true, "")
	prompt("three")
	log.step("lead", "")
	// A command moved to the background writes a bare result, and the
	// step that called it carries no build stamp.
	log.step("lead", "", message.ToolCall{ID: "b4", Name: "bash"})
	log.step("lead", oldBuild)
	// A refusal carries an error kind alone: neither a foreground command
	// nor a bare result, whatever its build.
	log.model, log.provider = "model-c", "provider-c"
	log.step("lead", newBuild, message.ToolCall{ID: "b5", Name: "bash"})
	result("lead", "b4", "bash", false, `{"background":true,"shell_id":"x"}`)
	result("lead", "b5", "bash", true, `{"error_kind":"permission_denied"}`)

	r := log.report(Options{})
	require.Equal(t, []Group{
		{
			Model: "model-b", Provider: "provider-b", Prompts: 2, MedianSteps: 1,
			ToolCalls: 5, ToolErrorRate: 0.4, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 2, ShellUnstamped: 1,
		},
		{
			Model: "model-a", Provider: "provider-a", Prompts: 1, MedianSteps: 2,
			ToolCalls: 5, ToolErrorRate: 0.4, Edits: 1, EditFailureRate: 1,
			ShellResults: 3, ShellCommands: 3, ShellFailureRate: 2.0 / 3.0,
		},
		{
			Model: "model-c", Provider: "provider-c",
			ToolCalls: 1, ToolErrorRate: 1, ShellResults: 1,
		},
	}, r.ByModel)
	require.Equal(t, []Group{
		{
			Build: newBuild, Prompts: 1, MedianSteps: 2,
			ToolCalls: 6, ToolErrorRate: 0.5, Edits: 1, EditFailureRate: 1,
			ShellResults: 4, ShellCommands: 3, ShellFailureRate: 2.0 / 3.0,
		},
		{
			Build: oldBuild, Prompts: 1, MedianSteps: 1,
			ToolCalls: 4, ToolErrorRate: 0.5, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 1,
		},
		// The third prompt's first step carries no build.
		{Build: "unstamped", Prompts: 1, MedianSteps: 4, ToolCalls: 1, ShellResults: 1, ShellUnstamped: 1},
	}, r.ByBuild)

	// The shell column reads as the Shell section does: a rate over the
	// group's foreground commands, "unstamped" for the results of a build
	// that never stamps, and a dash for a group with neither.
	var modelText, buildText bytes.Buffer
	writeGroups(&modelText, "By model", r.ByModel, false)
	writeGroups(&buildText, "By build", r.ByBuild, true)
	require.Equal(t, "66.67%", shellColumn(t, modelText.String(), "model-a\tprovider-a"))
	require.Equal(t, "unstamped", shellColumn(t, modelText.String(), "model-b\tprovider-b"))
	require.Equal(t, "66.67%", shellColumn(t, buildText.String(), newBuild))
	require.Equal(t, "-", shellColumn(t, modelText.String(), "model-c\tprovider-c"),
		"a refusal is neither a command nor an unstamped result")
	require.Equal(t, "-", shellColumn(t, buildText.String(), oldBuild),
		"its bare result comes from a build that stamps")
	require.Equal(t, "unstamped", shellColumn(t, buildText.String(), "unstamped"))

	// The columns name models, providers and builds, never a tool: a
	// model can invent any tool name.
	require.NotContains(t, modelText.String()+buildText.String(), "made_up_tool")
}

// shellColumn returns the last field of the rendered row named name.
func shellColumn(t *testing.T, text, name string) string {
	t.Helper()
	for line := range strings.SplitSeq(text, "\n") {
		rest, ok := strings.CutPrefix(line, "  "+name+"\t")
		if !ok {
			continue
		}
		fields := strings.Split(rest, "\t")
		return fields[len(fields)-1]
	}
	t.Fatalf("no row named %q in:\n%s", name, text)
	return ""
}

func TestGroupTextPrintsADashForNothingToMeasure(t *testing.T) {
	t.Parallel()

	// The second group has prompts but no calls and no edits; the third
	// has calls and edits but no prompt.
	var text bytes.Buffer
	writeGroups(&text, "By build", []Group{
		{
			Build: "old", Prompts: 2, MedianSteps: 1, ToolCalls: 2, ToolErrorRate: 0.5,
			Edits: 1, EditFailureRate: 1, ShellResults: 1, ShellCommands: 1, ShellFailureRate: 0.5,
		},
		{Build: "unstamped", Prompts: 1, MedianSteps: 3},
		{Build: "new", ToolCalls: 1, ToolErrorRate: 1, Edits: 1, EditFailureRate: 1},
	}, true)
	require.Contains(t, text.String(), "  old\t2\t1\t50.00%\t100.00%\t50.00%\n")
	require.Contains(t, text.String(), "  unstamped\t1\t3\t-\t-\t-\n",
		"no calls, no edits and no shell result")
	require.Contains(t, text.String(), "  new\t0\t-\t100.00%\t100.00%\t-\n",
		"no prompt to measure steps over")
}

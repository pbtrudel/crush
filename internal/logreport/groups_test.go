package logreport

import (
	"bytes"
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
	log.lastStep("lead", newBuild, message.TurnEndCompleted)
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
	log.step("lead", "")
	log.step("lead", oldBuild)

	r := log.report(Options{})
	require.Equal(t, []Group{
		{
			Model: "model-b", Provider: "provider-b", Prompts: 2, MedianSteps: 1,
			ToolCalls: 4, ToolErrorRate: 0.5, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 1,
		},
		{
			Model: "model-a", Provider: "provider-a", Prompts: 1, MedianSteps: 2,
			ToolCalls: 4, ToolErrorRate: 0.25, Edits: 1, EditFailureRate: 1,
			ShellResults: 2, ShellCommands: 2, ShellFailureRate: 0.5,
		},
	}, r.ByModel)
	require.Equal(t, []Group{
		{
			Build: newBuild, Prompts: 1, MedianSteps: 2,
			ToolCalls: 4, ToolErrorRate: 0.25, Edits: 1, EditFailureRate: 1,
			ShellResults: 2, ShellCommands: 2, ShellFailureRate: 0.5,
		},
		{
			Build: oldBuild, Prompts: 1, MedianSteps: 1,
			ToolCalls: 4, ToolErrorRate: 0.5, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 1,
		},
		// The third prompt's first step carries no build.
		{Build: "unstamped", Prompts: 1, MedianSteps: 3},
	}, r.ByBuild)

	// The columns name models, providers and builds, never a tool: a
	// model can invent any tool name.
	var text bytes.Buffer
	writeGroups(&text, "By model", r.ByModel, false)
	writeGroups(&text, "By build", r.ByBuild, true)
	require.NotContains(t, text.String(), "made_up_tool")
	require.Contains(t, text.String(), "unstamped", "a shell rate without stamps")
}

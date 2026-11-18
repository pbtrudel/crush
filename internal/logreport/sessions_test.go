package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestSessionsByKind(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	// These ids sort before their parent's, so the pass reads them first
	// and resolves their kinds once it is over.
	log.child("a1$$call-fetch", "lead")
	log.child("a2$$call-agent", "lead")
	log.child("a3$$call-dispatch", "lead")
	// The call that created it is not in the window.
	log.child("a4$$call-gone", "lead")
	log.child("team-reviewer", "lead")
	log.child("title-lead", "lead")
	// A child whose id has none of the known forms.
	log.child("other", "lead")

	log.step("lead", newBuild,
		message.ToolCall{ID: "call-fetch", Name: "agentic_fetch"},
		message.ToolCall{ID: "call-agent", Name: "agent"},
		message.ToolCall{ID: "call-dispatch", Name: "agent_dispatch"},
	)
	log.add("lead", message.User, message.TextContent{Text: "go on"})
	log.step("lead", newBuild)
	// A summary is not a step.
	log.summary("lead", message.TextContent{Text: "summary"})
	log.step("a1$$call-fetch", newBuild)
	log.step("a2$$call-agent", newBuild)
	log.step("a2$$call-agent", newBuild)
	log.step("a3$$call-dispatch", newBuild)
	log.add("a4$$call-gone", message.User, message.TextContent{Text: "brief"})
	log.step("team-reviewer", newBuild)
	log.step("team-reviewer", newBuild)
	log.step("team-reviewer", newBuild)
	log.step("title-lead", newBuild)
	log.add("other", message.User, message.TextContent{Text: "brief"})

	require.Equal(t, []SessionKind{
		{Kind: "subagent", Sessions: 2, Steps: 3, StepShare: 0.3},
		{Kind: "unknown", Sessions: 2},
		{Kind: "agentic_fetch", Sessions: 1, Steps: 1, StepShare: 0.1},
		{Kind: "teammate", Sessions: 1, Steps: 3, StepShare: 0.3},
		{Kind: "title", Sessions: 1, Steps: 1, StepShare: 0.1},
		{Kind: "top_level", Sessions: 1, Steps: 2, StepShare: 0.2},
	}, log.report(Options{}).Sessions.ByKind)
}

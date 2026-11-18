package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestTurnsWithChildSessions(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	// "a$$c1" sorts before "lead", so the pass reads it first.
	log.child("a$$c1", "lead")
	log.child("team-x", "lead")
	// A subagent of the teammate counts toward the lead's prompt.
	log.child("b$$c2", "team-x")
	// The parent of this one has no message in the window.
	log.child("c$$c3", "gone")
	call := message.ToolCall{ID: "c", Name: "glob"}

	// Written before the lead's first prompt: in no segment.
	log.step("a$$c1", newBuild, call)
	log.add("lead", message.User, message.TextContent{Text: "one"})
	log.step("lead", newBuild, message.ToolCall{ID: "c1", Name: "agent"})
	log.step("a$$c1", newBuild, call, call)
	log.step("a$$c1", newBuild)
	log.step("team-x", newBuild, call)
	log.add("lead", message.User, message.TextContent{Text: "two"})
	log.step("b$$c2", newBuild, call, call, call)
	log.step("team-x", newBuild)
	log.step("lead", newBuild)
	log.step("c$$c3", newBuild, call)

	turns := log.report(Options{}).Turns
	require.Equal(t, Spread{Median: 1, P90: 1, Max: 1}, turns.Steps)
	require.Equal(t, Spread{Median: 0, P90: 1, Max: 1}, turns.ToolCalls)
	// With child sessions, steps per prompt: 1 + 3 = 4, then 1 + 2 = 3.
	// Tool calls: 1 + 3 = 4, then 0 + 3 = 3.
	require.Equal(t, Spread{Median: 3, P90: 4, Max: 4}, turns.TreeSteps)
	require.Equal(t, Spread{Median: 3, P90: 4, Max: 4}, turns.TreeToolCalls)
}

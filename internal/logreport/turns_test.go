package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestTurnsPromptsHiddenTurnsAndContinuations(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	log.child("team-x", "lead")
	prompt := func(text string) {
		log.add("lead", message.User, message.TextContent{Text: text})
	}
	hidden := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text, Hidden: true})
	}
	call := message.ToolCall{ID: "c", Name: "glob"}

	hidden("lead", "<env_snapshot>")
	prompt("one")
	log.step("lead", newBuild, call, call)
	// Injections and hidden turns stay inside the prompt's segment.
	hidden("lead", "<system_reminder>x")
	log.step("lead", newBuild, call)
	hidden("lead", "Implement the plan.")
	log.lastStep("lead", newBuild, message.TurnEndContextGuard, call)
	// The continuation is known by the stamp on the last step before it,
	// looking past the summary and the injections.
	log.summary("lead", message.TextContent{Text: "summary"})
	hidden("lead", "<system_reminder>x")
	prompt("The previous session was interrupted because it got too long")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)

	prompt("two")
	// A blocked compaction still queues the prompt again.
	log.lastStep("lead", newBuild, message.TurnEndCompactionBlocked, call, call, call)
	prompt("The previous session was interrupted because it got too long")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)
	// Two prompts in a row: the first caused no step.
	prompt("three")
	prompt("four")
	log.lastStep("lead", newBuild, message.TurnEndCompactionFailed)
	// A failed compaction ends the turn, so the next row is a prompt.
	prompt("five")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)

	// A child session's rows are not prompts, and its runs are not
	// top-level runs.
	log.add("team-x", message.User, message.TextContent{Text: "brief"})
	hidden("team-x", "Implement the plan.")
	log.lastStep("team-x", newBuild, message.TurnEndLoopGuard)

	turns := log.report(Options{}).Turns
	require.Equal(t, 5, turns.Prompts)
	require.Equal(t, 1, turns.HiddenTurns)
	require.Equal(t, 2, turns.Continuations)
	require.Equal(t, 1, turns.ChildRows)
	// Steps per prompt: 4, 2, 0, 1, 1. Tool calls: 4, 3, 0, 0, 0.
	require.Equal(t, Spread{Median: 1, P90: 4, Max: 4}, turns.Steps)
	require.Equal(t, Spread{Median: 0, P90: 4, Max: 4}, turns.ToolCalls)
	require.Equal(t, map[string]int{
		"context_guard":      1,
		"compaction_blocked": 1,
		"compaction_failed":  1,
		"completed":          3,
	}, turns.RunEnds)
}

func TestTurnsWithoutStamps(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("old", "title")
	log.session("new", "title")
	prompt := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text})
	}
	// A session that a goal started: its steps come before any prompt and
	// belong to none.
	log.add("old", message.User, message.TextContent{Text: "You are pursuing a standing goal", Hidden: true})
	log.step("old", oldBuild)
	log.step("old", oldBuild)
	prompt("old", "one")
	log.step("old", oldBuild)
	log.step("old", oldBuild)
	// Written before the stamps, a continuation counts as a prompt.
	prompt("old", "The previous session was interrupted because it got too long")
	log.step("old", "")
	prompt("old", "no step follows")
	// A build that stamps turn ends left one run without a stamp, as a
	// crash does: that prompt is not unstamped.
	prompt("new", "one")
	log.lastStep("new", newBuild, message.TurnEndCompleted)
	prompt("new", "two")
	log.step("new", newBuild)

	turns := log.report(Options{}).Turns
	require.Equal(t, 5, turns.Prompts)
	require.Equal(t, 1, turns.HiddenTurns)
	require.Zero(t, turns.Continuations)
	// Steps per prompt: 2, 1, 0, 1, 1.
	require.Equal(t, Spread{Median: 1, P90: 2, Max: 2}, turns.Steps)
	require.Equal(t, map[string]int{"completed": 1, "unstamped": 2}, turns.RunEnds)
}

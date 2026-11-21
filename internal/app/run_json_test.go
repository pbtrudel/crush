package app

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/version"
)

func userRow(sessionID, text string, hidden bool) message.Message {
	return message.Message{
		SessionID: sessionID,
		Role:      message.User,
		Parts:     []message.ContentPart{message.TextContent{Text: text, Hidden: hidden}},
	}
}

// stepRow is one assistant step. A non-empty end marks the last step of a
// run; tools is how many tool calls the step made.
func stepRow(sessionID, text string, tools int, end message.TurnEnd, usage *message.RequestUsage) message.Message {
	parts := []message.ContentPart{message.TextContent{Text: text}}
	for range tools {
		parts = append(parts, message.ToolCall{ID: "call", Name: "bash", Finished: true})
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonEndTurn, Usage: usage, TurnEnd: end})
	return message.Message{
		SessionID: sessionID,
		Role:      message.Assistant,
		Parts:     parts,
		Model:     "model-" + sessionID,
		Provider:  "provider-" + sessionID,
	}
}

// The prompt's last run decides the reason. A wake-up run during the wait
// (hidden rows only) and a teammate's run count in the tree, not in the
// reason.
func TestSummarizeRunReadsThePromptsLastRun(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix the bug.", false),
		stepRow("root", "", 1, "", &message.RequestUsage{InputTokens: 100, CacheReadTokens: 50, CacheWriteTokens: 20, OutputTokens: 10}),
		userRow("root", "<system_reminder>Keep going.</system_reminder>", true),
		{SessionID: "root", Role: message.Tool},
		stepRow("root", "\nDone.\n", 0, message.TurnEndCompleted, &message.RequestUsage{InputTokens: 10, CacheReadTokens: 150, OutputTokens: 5}),
		userRow("root", "<background_job_notice>job 1 ended</background_job_notice>", true),
		stepRow("root", "", 0, message.TurnEndEmpty, nil),
		userRow("team-1", "Write the report.", false),
		stepRow("team-1", "Report.", 2, message.TurnEndCompleted, &message.RequestUsage{InputTokens: 7, OutputTokens: 3, Estimated: true}),
	}

	result := summarizeRun("root", rows)

	require.Equal(t, runSchema, result.Schema)
	require.Equal(t, "root", result.SessionID)
	require.Equal(t, message.TurnEndCompleted, result.Reason)
	require.Equal(t, "Done.", result.Text)
	require.Equal(t, "model-root", result.Model)
	require.Equal(t, "provider-root", result.Provider)
	require.Equal(t, version.BuildStamp(), result.Build)
	require.Equal(t, runCounts{Steps: 3, ToolCalls: 1, Tokens: runTokens{Input: 110, CacheRead: 200, CacheWrite: 20, Output: 15}}, result.Session)
	require.Equal(t, 2, result.Tree.Sessions)
	require.Equal(t, runCounts{Steps: 4, ToolCalls: 3, Tokens: runTokens{Input: 117, CacheRead: 200, CacheWrite: 20, Output: 18}}, result.Tree.runCounts)
	require.Equal(t, map[message.TurnEnd]int{message.TurnEndCompleted: 2, message.TurnEndEmpty: 1}, result.Tree.TurnEnds)
	require.True(t, result.Estimated, "the teammate's usage was estimated")
}

// A run cut by the context guard is continued as a new run whose opening
// row the harness writes visibly. The continuation decides the reason.
func TestSummarizeRunFollowsAContinuation(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Refactor it.", false),
		stepRow("root", "", 1, message.TurnEndContextGuard, nil),
		{SessionID: "root", Role: message.Assistant, IsSummaryMessage: true, Parts: []message.ContentPart{
			message.TextContent{Text: "Summary."},
			message.Finish{Reason: message.FinishReasonEndTurn, Usage: &message.RequestUsage{InputTokens: 7}},
		}},
		userRow("root", "The previous session was interrupted because it got too long, the initial user request was: `Refactor it.`", false),
		stepRow("root", "Refactored.", 0, message.TurnEndCompleted, nil),
	}

	result := summarizeRun("root", rows)

	require.Equal(t, message.TurnEndCompleted, result.Reason)
	require.Equal(t, "Refactored.", result.Text)
	require.Equal(t, 2, result.Session.Steps, "a summary is not a step")
	require.Equal(t, int64(7), result.Session.Tokens.Input, "the summary's usage still counts")
	require.Equal(t, map[message.TurnEnd]int{message.TurnEndContextGuard: 1, message.TurnEndCompleted: 1}, result.Tree.TurnEnds)
	require.False(t, result.Estimated)
}

// The turn-end gate's row reopens the prompt's run, so the continuation's
// ending decides the reason and the exit code.
func TestSummarizeRunFollowsAGateContinuation(t *testing.T) {
	t.Parallel()

	rows := func(continuation message.TurnEnd) []message.Message {
		return []message.Message{
			userRow("root", "Fix it.", false),
			stepRow("root", "Done.", 0, message.TurnEndCompleted, nil),
			userRow("root", "<turn_end_gate>\nCrush checked your turn.", true),
			stepRow("root", "Partial.", 0, continuation, nil),
		}
	}

	result := summarizeRun("root", rows(message.TurnEndMaxTokens))
	require.Equal(t, message.TurnEndMaxTokens, result.Reason)
	require.Equal(t, "Partial.", result.Text)
	require.Equal(t, 2, runExitCode(result.Reason), "a continuation that stopped short exits 2, not 0")

	// A continuation that hit the step limit ends the command the same way.
	result = summarizeRun("root", rows(message.TurnEndStepLimit))
	require.Equal(t, message.TurnEndStepLimit, result.Reason)
	require.Equal(t, 2, runExitCode(result.Reason))
}

// A run the gate never continued prints the object it printed before the
// gate existed: no gate key, and the same keys as then.
func TestRunResultOmitsTheGateWhenUnused(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix it.", false),
		stepRow("root", "Done.", 0, message.TurnEndCompleted, nil),
	}

	result := summarizeRun("root", rows)
	require.Nil(t, result.Gate)

	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"gate"`)
	var object map[string]any
	require.NoError(t, json.Unmarshal(data, &object))
	fields := make([]string, 0, len(object))
	for field := range object {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	require.Equal(t, []string{
		"build", "cost", "duration_ms", "estimated", "model", "provider", "reason",
		"schema", "session", "session_id", "text", "tree",
	}, fields)
}

// A subagent report folded back into the session opens no run, so the
// prompt's run still decides the reason; a hidden background notice opens
// none either.
func TestSummarizeRunIgnoresASubagentReport(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix the bug.", false),
		stepRow("root", "Dispatched.", 0, message.TurnEndCompleted, nil),
		{SessionID: "root", Role: message.User, Parts: []message.ContentPart{message.SubAgentReport{Label: "worker", Output: "done"}}},
		stepRow("root", "Final answer.", 0, message.TurnEndMaxTokens, nil),
		userRow("root", "<background_job_notice>job 1 ended</background_job_notice>", true),
		stepRow("root", "", 0, message.TurnEndEmpty, nil),
	}

	result := summarizeRun("root", rows)

	require.Equal(t, message.TurnEndCompleted, result.Reason)
	require.Equal(t, "Dispatched.", result.Text)
}

func TestSummarizeRunWithoutAStampedRun(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix the bug.", false),
		stepRow("root", "", 1, "", nil),
	}

	result := summarizeRun("root", rows)

	require.Equal(t, message.TurnEndError, result.Reason)
	require.Empty(t, result.Text)
	require.Equal(t, 1, result.Tree.Sessions)
	require.Empty(t, result.Tree.TurnEnds)
}

// The field names are the contract scripts read.
func TestRunResultJSON(t *testing.T) {
	t.Parallel()
	result := runResult{
		Schema: 1, SessionID: "root", Reason: message.TurnEndCompleted, Text: "ok",
		DurationMs: 1200, Model: "m", Provider: "p", Build: "b",
		Session: runCounts{Steps: 1, Tokens: runTokens{Input: 5}},
		Tree: runTree{
			Sessions: 1, runCounts: runCounts{Steps: 1, Tokens: runTokens{Input: 5}},
			TurnEnds: map[message.TurnEnd]int{message.TurnEndCompleted: 1}, UndeliveredReports: 2,
		},
		Gate: &runGate{Continuations: 1, Claims: 2, UnbackedClaims: 1},
		Cost: 0.5,
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"schema": 1, "session_id": "root", "reason": "completed", "text": "ok",
		"duration_ms": 1200, "model": "m", "provider": "p", "build": "b",
		"session": {"steps": 1, "tool_calls": 0, "tokens": {"input": 5, "cache_read": 0, "cache_write": 0, "output": 0}},
		"tree": {"sessions": 1, "steps": 1, "tool_calls": 0, "tokens": {"input": 5, "cache_read": 0, "cache_write": 0, "output": 0},
			"turn_ends": {"completed": 1}, "undelivered_reports": 2},
		"gate": {"continuations": 1, "claims": 2, "unbacked_claims": 1},
		"estimated": false, "cost": 0.5
	}`, string(data))
}

// The tree is the session and every session below it, at any depth; a
// session outside it is left out.
func TestTreeRowsListsTheSessionsTree(t *testing.T) {
	t.Parallel()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)

	root, err := sessions.Create(t.Context(), "Root")
	require.NoError(t, err)
	child, err := sessions.CreateTaskSession(t.Context(), "child", root.ID, "Child")
	require.NoError(t, err)
	grandchild, err := sessions.CreateTaskSession(t.Context(), "grandchild", child.ID, "Grandchild")
	require.NoError(t, err)
	other, err := sessions.Create(t.Context(), "Other")
	require.NoError(t, err)
	for _, id := range []string{root.ID, child.ID, grandchild.ID, other.ID} {
		_, err := messages.Create(t.Context(), id, message.CreateMessageParams{
			Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
	}

	rows, err := treeRows(t.Context(), conn, messages, root.ID)
	require.NoError(t, err)
	var got []string
	for _, row := range rows {
		got = append(got, row.SessionID)
	}
	require.ElementsMatch(t, []string{root.ID, child.ID, grandchild.ID}, got)
}

// A limit that stopped a teammate after the session completed is the
// reason; the text stays the session's own.
func TestSummarizeRunReportsALimitInTheTree(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix the bug.", false),
		stepRow("root", "Started the worker.", 0, message.TurnEndCompleted, nil),
		userRow("team-1", "Do the work.", false),
		stepRow("team-1", "", 1, message.TurnEndStepLimit, nil),
	}

	result := summarizeRun("root", rows)
	require.Equal(t, message.TurnEndStepLimit, result.Reason)
	require.Equal(t, "Started the worker.", result.Text)

	rows = append(rows,
		userRow("team-2", "Review it.", false),
		stepRow("team-2", "", 1, message.TurnEndTimeLimit, nil),
	)
	require.Equal(t, message.TurnEndTimeLimit, summarizeRun("root", rows).Reason, "the deadline is read first")
}

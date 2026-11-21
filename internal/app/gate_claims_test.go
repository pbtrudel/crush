package app

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// commandStep is an assistant row that answered with text and, when name is
// set, called one tool with a JSON input.
func commandStep(sessionID, text, id, name, input string) message.Message {
	parts := []message.ContentPart{message.TextContent{Text: text}}
	if name != "" {
		parts = append(parts, message.ToolCall{ID: id, Name: name, Input: input, Finished: true})
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonEndTurn, TurnEnd: message.TurnEndCompleted})
	return message.Message{SessionID: sessionID, Role: message.Assistant, Parts: parts}
}

// toolAnswer is the tool row that answers one call.
func toolAnswer(sessionID, id, name string, isError bool) message.Message {
	return message.Message{SessionID: sessionID, Role: message.Tool, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: id, Name: name, IsError: isError},
	}}
}

// answeredStep is a step whose one tool call succeeded, unless isError.
func answeredStep(sessionID, id, name, input string, isError bool) []message.Message {
	return []message.Message{
		commandStep(sessionID, "", id, name, input),
		toolAnswer(sessionID, id, name, isError),
	}
}

// verifiedAnswer renders an answer with a Verified block holding one
// claim.
func verifiedAnswer(claim string) string {
	return "Done.\n\nAsked: fix it\nVerified:\n- `" + claim + "`: it passed\nUnverified: nothing"
}

func TestGateClaims(t *testing.T) {
	t.Parallel()

	t.Run("a claim that matches a command exactly", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go test ./..."}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a claim with extra whitespace", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go   test ./..."}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test   ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a claim that is part of a longer command", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"cd internal/app && go test ./... -race"}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a claim that names a tool that was called", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "lsp_diagnostics", `{"file_path":"a.go"}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("lsp_diagnostics on a.go"), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a tool name only counts as a whole word", func(t *testing.T) {
		t.Parallel()
		// "ls" sits inside "tools", and calling ls verified nothing
		// about a test run.
		rows := answeredStep("root", "c1", "ls", `{"path":"."}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./tools/..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Equal(t, 1, unbacked)
	})

	t.Run("a tool name in the middle of a claim still counts", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "lsp_diagnostics", `{"file_path":"a.go"}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("checked lsp_diagnostics for a.go"), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a claim no call backs", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Equal(t, 1, unbacked)
	})

	t.Run("a claim a failed call backs", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go test ./..."}`, true)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Equal(t, 1, unbacked)
	})

	t.Run("verified none claims nothing", func(t *testing.T) {
		t.Parallel()
		rows := append(answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false),
			commandStep("root", "Done.\nVerified: none\nUnverified: the tests", "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Zero(t, claims)
		require.Zero(t, unbacked)
	})

	t.Run("a missing block claims nothing", func(t *testing.T) {
		t.Parallel()
		rows := append(answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false),
			commandStep("root", "Done. Everything works.", "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Zero(t, claims)
		require.Zero(t, unbacked)
	})

	t.Run("several claims count separately", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false)
		answer := "Done.\n\nVerified:\n- `go build ./...`: it compiled\n- `go vet ./...`: it passed\n- `go test ./...`: it passed\nUnverified: nothing"
		rows = append(rows, commandStep("root", answer, "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 3, claims)
		require.Equal(t, 2, unbacked)
	})

	// The continuation's answer is the one that counts, and the commands of
	// the first run still back it.
	t.Run("a continuation's claims read the first run's commands", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("root", "c1", "bash", `{"command":"go test ./..."}`, false)
		rows = append(rows, commandStep("root", "Done.", "", "", ""))
		rows = append(rows, userRow("root", "<turn_end_gate>\nCrush checked your turn.", true))
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Zero(t, unbacked)
	})

	t.Run("another session's calls do not back a claim", func(t *testing.T) {
		t.Parallel()
		rows := answeredStep("child", "c1", "bash", `{"command":"go test ./..."}`, false)
		rows = append(rows, commandStep("root", verifiedAnswer("go test ./..."), "", "", ""))

		claims, unbacked := gateClaims(rows, "root")
		require.Equal(t, 1, claims)
		require.Equal(t, 1, unbacked)
	})
}

// The counts reach the printed object as the gate object, with zeros when
// no gate row was written.
func TestSummarizeRunGateCounts(t *testing.T) {
	t.Parallel()

	rows := answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false)
	rows = append(rows,
		commandStep("root", "Done.", "", "", ""),
		userRow("root", "<turn_end_gate>\nCrush checked your turn.", true),
		commandStep("root", verifiedAnswer("go test ./..."), "", "", ""),
	)

	result := summarizeRun("root", rows)
	require.NotNil(t, result.Gate)
	require.Equal(t, 1, result.Gate.Continuations)
	require.Equal(t, 1, result.Gate.Claims)
	require.Equal(t, 1, result.Gate.UnbackedClaims)

	// A run the gate never continued carries no gate object at all.
	plain := summarizeRun("root", append(answeredStep("root", "c1", "bash", `{"command":"go build ./..."}`, false),
		commandStep("root", "Done.", "", "", "")))
	require.Nil(t, plain.Gate)
}

// The Verified block survives the shapes a model writes it in: a bold
// heading, an item on the heading's line, and any of the three bullet
// markers.
func TestVerifiedClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"dash bullets", "Verified:\n- `go build ./...`: ok\n- `go vet ./...`: ok", []string{"go build ./...", "go vet ./..."}},
		{"bold heading", "**Verified:**\n- `go build ./...`: ok", []string{"go build ./..."}},
		{"an item on the heading line", "Verified: `go build ./...` compiled", []string{"go build ./..."}},
		{"star bullets", "Verified:\n* `go build ./...`\n* `go vet ./...`", []string{"go build ./...", "go vet ./..."}},
		{"numbered bullets", "Verified:\n1. `go build ./...`\n2. `go vet ./...`", []string{"go build ./...", "go vet ./..."}},
		{"verified none", "Verified: none\nUnverified: the tests", nil},
		{"no block", "All done.", nil},
		{"prose ends the block", "Verified:\n- `go build ./...`: ok\n\nThat is all.", []string{"go build ./..."}},
		{"a claim with no command", "Verified:\n- I ran the tests", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, verifiedClaims(tt.text))
		})
	}
}

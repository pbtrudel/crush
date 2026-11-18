package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// newRunLogApp is an App with a real session log and one session in it.
func newRunLogApp(t *testing.T) (*App, session.Session) {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	app := &App{Sessions: session.NewService(q, conn), Messages: message.NewService(q), conn: conn}
	sess, err := app.Sessions.Create(t.Context(), "Run")
	require.NoError(t, err)
	return app, sess
}

// writeRows stores rows built by userRow and stepRow in the log.
func writeRows(t *testing.T, app *App, rows ...message.Message) {
	t.Helper()
	for _, row := range rows {
		_, err := app.Messages.Create(t.Context(), row.SessionID, message.CreateMessageParams{
			Role: row.Role, Parts: row.Parts, Model: row.Model, Provider: row.Provider,
		})
		require.NoError(t, err)
	}
}

func decodeRun(t *testing.T, output *bytes.Buffer) runResult {
	t.Helper()
	require.Equal(t, 1, strings.Count(output.String(), "\n"), "one object, one line")
	var result runResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	return result
}

// A continued session: what an earlier command wrote is left out, the
// teammate's run and the unread report are counted, and the cost is what
// the tree added during the command.
func TestRunJSONPrintsWhatTheCommandWrote(t *testing.T) {
	t.Parallel()
	app, sess := newRunLogApp(t)
	writeRows(t, app,
		userRow(sess.ID, "Earlier prompt.", false),
		stepRow(sess.ID, "Cut.", 0, message.TurnEndMaxTokens, nil),
	)
	require.NoError(t, app.Sessions.AddSessionUsage(t.Context(), sess.ID, 0, 0, 0.125))
	sess, err := app.Sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)

	start, err := app.startRun(t.Context(), sess)
	require.NoError(t, err)
	teammate, err := app.Sessions.CreateTaskSession(t.Context(), "team-1", sess.ID, "Teammate worker")
	require.NoError(t, err)
	writeRows(t, app,
		userRow(sess.ID, "Start the worker.", false),
		stepRow(sess.ID, "ok", 1, message.TurnEndCompleted, &message.RequestUsage{InputTokens: 40, OutputTokens: 2}),
		userRow(teammate.ID, "Write the report.", false),
		stepRow(teammate.ID, "Report.", 0, message.TurnEndCompleted, &message.RequestUsage{InputTokens: 60, OutputTokens: 8}),
	)
	require.NoError(t, app.Sessions.AddSessionUsage(t.Context(), sess.ID, 0, 0, 0.25))
	require.NoError(t, app.Sessions.AddSessionUsage(t.Context(), teammate.ID, 0, 0, 0.5))
	done := make(chan runResponse, 1)
	done <- runResponse{tree: agent.SessionTree{Team: agentteam.TreeStatus{UnreadByLead: 1}}}

	var output bytes.Buffer
	require.NoError(t, app.finishRunJSON(t.Context(), &output, sess.ID, start, done))

	result := decodeRun(t, &output)
	require.Equal(t, sess.ID, result.SessionID)
	require.Equal(t, message.TurnEndCompleted, result.Reason)
	require.Equal(t, "ok", result.Text)
	require.Equal(t, runCounts{Steps: 1, ToolCalls: 1, Tokens: runTokens{Input: 40, Output: 2}}, result.Session)
	require.Equal(t, 2, result.Tree.Sessions)
	require.Equal(t, runCounts{Steps: 2, ToolCalls: 1, Tokens: runTokens{Input: 100, Output: 10}}, result.Tree.runCounts)
	require.Equal(t, map[message.TurnEnd]int{message.TurnEndCompleted: 2}, result.Tree.TurnEnds)
	require.Equal(t, 1, result.Tree.UndeliveredReports)
	require.InDelta(t, 0.75, result.Cost, 1e-9)
	require.Empty(t, result.Error)
}

// The exit code follows the stamp in the log, and the object is printed
// whatever the outcome.
func TestRunJSONExitsByTheReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason message.TurnEnd
		runErr error
		code   int
	}{
		{reason: message.TurnEndCompleted},
		{reason: message.TurnEndToolStop, code: 2},
		{reason: message.TurnEndProviderError, runErr: errors.New("connection reset by peer"), code: 3},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			t.Parallel()
			app, sess := newRunLogApp(t)
			start, err := app.startRun(t.Context(), sess)
			require.NoError(t, err)
			writeRows(t, app,
				userRow(sess.ID, "Go.", false),
				stepRow(sess.ID, "", 0, tt.reason, nil),
			)
			done := make(chan runResponse, 1)
			done <- runResponse{err: tt.runErr}

			var output bytes.Buffer
			err = app.finishRunJSON(t.Context(), &output, sess.ID, start, done)

			result := decodeRun(t, &output)
			require.Equal(t, tt.reason, result.Reason)
			if tt.code == 0 {
				require.NoError(t, err)
				return
			}
			var exitErr *ExitError
			require.ErrorAs(t, err, &exitErr)
			require.Equal(t, tt.code, exitErr.Code)
			if tt.runErr != nil {
				require.Equal(t, tt.runErr.Error(), result.Error)
			}
		})
	}
}

// When the prompt never reached the log no run started: nothing is printed
// and the run's own error is returned, which exits 1.
func TestRunJSONPrintsNothingWithoutARun(t *testing.T) {
	t.Parallel()
	app, sess := newRunLogApp(t)
	start, err := app.startRun(t.Context(), sess)
	require.NoError(t, err)
	runErr := errors.New("coder agent not configured")
	done := make(chan runResponse, 1)
	done <- runResponse{err: runErr}

	var output bytes.Buffer
	err = app.finishRunJSON(t.Context(), &output, sess.ID, start, done)

	require.ErrorIs(t, err, runErr)
	var exitErr *ExitError
	require.False(t, errors.As(err, &exitErr))
	require.Empty(t, output.String())
}

// A signal stops the wait. The run still records its cancel and returns,
// and the object reports it; Execute exits 130 because its signal context
// is done.
func TestRunJSONAfterASignal(t *testing.T) {
	t.Parallel()
	app, sess := newRunLogApp(t)
	start, err := app.startRun(t.Context(), sess)
	require.NoError(t, err)
	writeRows(t, app,
		userRow(sess.ID, "Go.", false),
		stepRow(sess.ID, "", 0, message.TurnEndCanceled, nil),
	)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runErr := fmt.Errorf("failed to start agent processing stream: %w", context.Canceled)
	done := make(chan runResponse, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		done <- runResponse{err: runErr}
	}()

	var output bytes.Buffer
	err = app.finishRunJSON(ctx, &output, sess.ID, start, done)

	result := decodeRun(t, &output)
	require.Equal(t, message.TurnEndCanceled, result.Reason)
	require.Equal(t, runErr.Error(), result.Error, "the run's own response was awaited")
	var exitErr *ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 3, exitErr.Code)
}

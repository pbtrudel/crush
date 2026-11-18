package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// runSettleGrace bounds how long a signalled command waits for its run to
// record the cancel, so that the object it prints can report it.
const runSettleGrace = 5 * time.Second

// runStart is what the command knew before its run started.
type runStart struct {
	at   time.Time
	cost float64
	// written holds the rows already in the session's tree. The object
	// counts only the rows the command writes.
	written map[string]bool
}

// treeMessageIDsQuery lists the id of every row in a session's tree.
const treeMessageIDsQuery = `WITH RECURSIVE tree(id) AS (
	SELECT ?
	UNION
	SELECT sessions.id FROM sessions JOIN tree ON sessions.parent_session_id = tree.id
)
SELECT messages.id FROM messages JOIN tree ON messages.session_id = tree.id`

// startRun notes the state of the session's tree before the run starts.
func (app *App) startRun(ctx context.Context, sess session.Session) (runStart, error) {
	rows, err := app.conn.QueryContext(ctx, treeMessageIDsQuery, sess.ID)
	if err != nil {
		return runStart{}, err
	}
	defer rows.Close()
	start := runStart{at: time.Now(), cost: sess.Cost, written: map[string]bool{}}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return runStart{}, err
		}
		start.written[id] = true
	}
	if err := rows.Err(); err != nil {
		return runStart{}, err
	}
	return start, nil
}

// finishRunJSON waits for the run and its session tree, then prints the
// object. A signal stops the wait: the run is given a moment to record its
// cancel, and the process still exits 130.
func (app *App) finishRunJSON(ctx context.Context, output io.Writer, sessionID string, start runStart, done <-chan runResponse) error {
	var response runResponse
	select {
	case response = <-done:
	case <-ctx.Done():
		select {
		case response = <-done:
		case <-time.After(runSettleGrace):
			// The wait never ended, so undelivered_reports is not
			// measured and reads 0.
			response.err = ctx.Err()
		}
	}
	return app.printRunJSON(context.WithoutCancel(ctx), output, sessionID, start, response)
}

// printRunJSON prints the object for the rows the command wrote and
// returns the error that carries the exit code. When the prompt never
// reached the log no run started: nothing is printed and the run's error is
// returned, so the command exits 1.
func (app *App) printRunJSON(ctx context.Context, output io.Writer, sessionID string, start runStart, response runResponse) error {
	if err := app.Messages.FlushAll(ctx); err != nil {
		slog.Warn("Failed to flush messages before reading the run", "error", err)
	}
	rows, err := treeRows(ctx, app.conn, app.Messages, sessionID)
	if err != nil {
		return fmt.Errorf("failed to read the run from the session log: %w", err)
	}
	var written []message.Message
	started := false
	for _, row := range rows {
		if start.written[row.ID] {
			continue
		}
		written = append(written, row)
		started = started || row.SessionID == sessionID
	}
	if !started {
		return cmp.Or(response.err, errors.New("the run wrote nothing to the session log"))
	}
	result := summarizeRun(sessionID, written)
	result.DurationMs = time.Since(start.at).Milliseconds()
	result.Tree.UndeliveredReports = response.tree.Team.UnreadByLead
	// The session's cost already includes what its child sessions cost.
	if sess, err := app.Sessions.Get(ctx, sessionID); err != nil {
		slog.Warn("Failed to read the session's cost", "error", err)
	} else {
		result.Cost = sess.Cost - start.cost
	}
	if response.err != nil {
		result.Error = response.err.Error()
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("failed to print the run: %w", err)
	}
	return runExitError(result.Reason)
}

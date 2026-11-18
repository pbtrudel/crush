package app

import (
	"context"
	"database/sql"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/version"
)

// runSchema numbers the object `crush run --json` prints. Within one schema
// number fields are only added.
const runSchema = 1

// runTokens sums the usage the steps recorded on their finish parts.
type runTokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
}

// runCounts is what the steps of some sessions did during the command.
type runCounts struct {
	Steps     int       `json:"steps"`
	ToolCalls int       `json:"tool_calls"`
	Tokens    runTokens `json:"tokens"`
}

// add counts one assistant row and reports whether Crush estimated its
// usage. A summary row is not a step, but the tool calls and the tokens
// it recorded were spent, so they count.
func (c *runCounts) add(m message.Message) bool {
	if !m.IsSummaryMessage {
		c.Steps++
	}
	c.ToolCalls += len(m.ToolCalls())
	finish := m.FinishPart()
	if finish == nil || finish.Usage == nil {
		return false
	}
	c.Tokens.Input += finish.Usage.InputTokens
	c.Tokens.CacheRead += finish.Usage.CacheReadTokens
	c.Tokens.CacheWrite += finish.Usage.CacheWriteTokens
	c.Tokens.Output += finish.Usage.OutputTokens
	return finish.Usage.Estimated
}

// runTree is the command's session and every session started from it.
type runTree struct {
	Sessions int `json:"sessions"`
	runCounts
	TurnEnds           map[message.TurnEnd]int `json:"turn_ends"`
	UndeliveredReports int                     `json:"undelivered_reports"`
}

// runResult is the object `crush run --json` prints (measurement design,
// 3.2).
type runResult struct {
	Schema     int             `json:"schema"`
	SessionID  string          `json:"session_id"`
	Reason     message.TurnEnd `json:"reason"`
	Text       string          `json:"text"`
	DurationMs int64           `json:"duration_ms"`
	Model      string          `json:"model"`
	Provider   string          `json:"provider"`
	Build      string          `json:"build"`
	Session    runCounts       `json:"session"`
	Tree       runTree         `json:"tree"`
	Estimated  bool            `json:"estimated"`
	Cost       float64         `json:"cost"`
	Error      string          `json:"error,omitempty"`
}

// summarizeRun builds the object from the rows the command wrote in the
// tree of sessionID, each session's rows in log order. The duration, the
// cost, the unread reports and the error are not in these rows; the caller
// sets them.
//
// The reason and the text are those of the session's last run that a
// user row opened: the prompt, or its continuation after a compaction.
// A run the harness opened with hidden rows only, such as a wake-up by a
// background job during the wait, counts in the tree but never decides
// the reason, so whether such a run happened to start before the command
// ended cannot change how the command ends. Subagent reports open no run
// either, like hidden rows. Without a stamped run of the prompt the
// reason is error.
func summarizeRun(sessionID string, rows []message.Message) runResult {
	result := runResult{
		Schema:    runSchema,
		SessionID: sessionID,
		Reason:    message.TurnEndError,
		Build:     version.BuildStamp(),
		Tree:      runTree{TurnEnds: map[message.TurnEnd]int{}},
	}
	sessions := map[string]bool{sessionID: true}
	// opened is true once a user row that opensRun accepts started the
	// run that the next stamp ends.
	opened := false
	for _, m := range rows {
		sessions[m.SessionID] = true
		root := m.SessionID == sessionID
		switch m.Role {
		case message.User:
			if root && opensRun(m) {
				opened = true
			}
			continue
		case message.Assistant:
		default:
			continue
		}
		if result.Tree.add(m) {
			result.Estimated = true
		}
		if root {
			result.Session.add(m)
		}
		finish := m.FinishPart()
		if finish == nil || finish.TurnEnd == "" {
			continue
		}
		result.Tree.TurnEnds[finish.TurnEnd]++
		if !root {
			continue
		}
		if opened {
			result.Reason = finish.TurnEnd
			result.Text = strings.TrimSpace(m.Content().Text)
			result.Model, result.Provider = m.Model, m.Provider
		}
		opened = false
	}
	result.Tree.Sessions = len(sessions)
	return result
}

// opensRun reports whether a user row of the session starts a run that can
// decide the command's reason: the visible prompt, or its continuation
// after a compaction. Rows the harness writes do not: hidden notices and
// reminders, and subagent reports, which a non-interactive run cannot
// produce and which may be recorded without starting a run.
func opensRun(m message.Message) bool {
	if len(m.SubAgentReports()) > 0 {
		return false
	}
	return !m.Content().Hidden
}

// treeSessionsQuery lists a session and every session started from it.
// UNION skips a session already listed, so a parent cycle cannot loop.
const treeSessionsQuery = `WITH RECURSIVE tree(id) AS (
	SELECT ?
	UNION
	SELECT sessions.id FROM sessions JOIN tree ON sessions.parent_session_id = tree.id
)
SELECT id FROM tree`

// treeRows lists every row in the tree of sessionID, each session's rows
// in log order.
func treeRows(ctx context.Context, conn *sql.DB, messages message.Service, sessionID string) ([]message.Message, error) {
	rows, err := conn.QueryContext(ctx, treeSessionsQuery, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var all []message.Message
	for _, id := range ids {
		msgs, err := messages.List(ctx, id)
		if err != nil {
			return nil, err
		}
		all = append(all, msgs...)
	}
	return all, nil
}

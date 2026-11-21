package app

import (
	"context"
	"database/sql"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/tidwall/gjson"
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

// runGate is what the turn-end gate did during the command's prompt:
// the continuations it added, and the checks the answer claimed but no tool
// call backed.
type runGate struct {
	Continuations  int `json:"continuations"`
	Claims         int `json:"claims"`
	UnbackedClaims int `json:"unbacked_claims"`
}

// The tools the edits object counts. They are the names the session log
// records, as the log report reads them.
const (
	editToolName      = "edit"
	multiEditToolName = "multiedit"
)

// runEdits is what the edit and multiedit calls of the command did: how
// many it attempted, how many it refused and by which kind, and how many
// applied to a file not read first. The kinds split Refused, and a kind
// outside the log's closed list is other.
type runEdits struct {
	Attempted        int `json:"attempted"`
	Refused          int `json:"refused"`
	NotRead          int `json:"not_read"`
	ChangedSinceRead int `json:"changed_since_read"`
	NoMatch          int `json:"no_match"`
	MultipleMatches  int `json:"multiple_matches"`
	Other            int `json:"other"`
	UnreadApplied    int `json:"unread_applied"`
}

// editCounts counts the edit and multiedit calls of the run and their
// results, or returns nil when the run made no such call: a run without an
// edit prints the object it printed before the metric existed.
func editCounts(rows []message.Message) *runEdits {
	var edits runEdits
	// calls holds the ids of the run's edit calls, so a result whose call
	// is outside the run is not counted.
	calls := map[string]bool{}
	for _, m := range rows {
		switch m.Role {
		case message.Assistant:
			for _, tc := range m.ToolCalls() {
				if tc.Name != editToolName && tc.Name != multiEditToolName {
					continue
				}
				edits.Attempted++
				calls[tc.ID] = true
			}
		case message.Tool:
			for _, tr := range m.ToolResults() {
				if !calls[tr.ToolCallID] {
					continue
				}
				if !tr.IsError {
					if gjson.Get(tr.Metadata, "unread_edit").Bool() {
						edits.UnreadApplied++
					}
					continue
				}
				edits.Refused++
				switch gjson.Get(tr.Metadata, "error_kind").String() {
				case "not_read":
					edits.NotRead++
				case "changed_since_read":
					edits.ChangedSinceRead++
				case "text_not_found":
					edits.NoMatch++
				case "multiple_matches":
					edits.MultipleMatches++
				default:
					edits.Other++
				}
			}
		}
	}
	if edits.Attempted == 0 {
		return nil
	}
	return &edits
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
	// Gate is set only for a run the turn-end gate continued, so a run
	// without the gate prints the object it printed before it existed.
	Gate *runGate `json:"gate,omitempty"`
	// Edits is set only for a run that called edit or multiedit, so a run
	// without one prints the object it printed before the metric existed.
	Edits     *runEdits `json:"edits,omitempty"`
	Estimated bool      `json:"estimated"`
	Cost      float64   `json:"cost"`
	Error     string    `json:"error,omitempty"`
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
	// A limit that stopped any run in the tree is the reason, so a turn
	// it cut short in a child session is not reported as completed. The
	// deadline is read first, as in the turn-end rules.
	for _, limit := range []message.TurnEnd{message.TurnEndTimeLimit, message.TurnEndStepLimit} {
		if result.Tree.TurnEnds[limit] > 0 {
			result.Reason = limit
			break
		}
	}
	result.Tree.Sessions = len(sessions)
	result.Edits = editCounts(rows)
	// Only a run the gate continued has anything to report, and only then
	// is the answer worth scanning for claims it never backed.
	if continuations := gateRowCount(rows, sessionID); continuations > 0 {
		claims, unbacked := gateClaims(rows, sessionID)
		result.Gate = &runGate{
			Continuations:  continuations,
			Claims:         claims,
			UnbackedClaims: unbacked,
		}
	}
	return result
}

// opensRun reports whether a user row of the session starts a run that can
// decide the command's reason: the visible prompt, its continuation after a
// compaction, or the turn-end gate's row, whose run ends the prompt. Other
// rows the harness writes do not: hidden notices and reminders, and subagent
// reports, which a non-interactive run cannot produce and which may be
// recorded without starting a run.
func opensRun(m message.Message) bool {
	if len(m.SubAgentReports()) > 0 {
		return false
	}
	if m.Content().Hidden {
		return strings.HasPrefix(m.Content().Text, gateOpenTag)
	}
	return true
}

// gateOpenTag opens the hidden row the turn-end gate writes. It is the same
// tag the log report knows, and the row that reopens the prompt's run.
const gateOpenTag = "<turn_end_gate>"

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

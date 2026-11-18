// Package logreport reads one project's session log and reports counts,
// rates and the names of tools, kinds and builds. It never reports text
// from a message, a title, a path or a session id.
package logreport

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Schema is the version of the JSON output. Within one schema number
// fields are only added.
const Schema = 1

// Options bounds the messages a report reads. A zero bound is open.
type Options struct {
	// Since is the first instant included.
	Since time.Time
	// Until is the first instant excluded.
	Until time.Time
}

// Report is the result of one pass over the session log.
type Report struct {
	Schema int    `json:"schema"`
	Window Window `json:"window"`
	Tools  Tools  `json:"tools"`
	Shell  Shell  `json:"shell"`
	Edits  Edits  `json:"edits"`
	Reads  Reads  `json:"reads"`
}

// section is one part of the report. The pass shows it every message it
// could decode, in session_id, created_at, rowid order, and tells it when
// a session ends. Besides its counters a section keeps state for the
// current session only. finish writes its figures into the report once
// the pass is over, when the pass knows which builds stamp their results.
type section interface {
	message(p *pass, m *message.Message)
	endSession(p *pass)
	finish(p *pass, r *Report)
}

// newSections returns the report's sections in the order they print.
func newSections() []section {
	return []section{
		&windowSection{builds: map[string]*Build{}},
		newToolsSection(),
		&shellSection{bare: map[string]int{}},
		newEditsSection(),
		&readsSection{seen: map[string]bool{}},
	}
}

// pass holds what the sections share.
type pass struct {
	opts     Options
	roles    map[string]int
	sessions int
	skipped  int
	first    int64
	last     int64
	// calls holds the current session's tool calls by id.
	calls map[string]call
	// stamping holds the builds whose tool results carry a stamp:
	// exit_code or interrupted on a shell result, error_kind on any.
	stamping map[string]bool
}

// call is what the pass keeps of a tool call until its session ends.
type call struct {
	name string
	// build is the build stamp of the step that made the call.
	build string
}

const messagesQuery = `SELECT session_id, role, parts, COALESCE(model, ''),
	COALESCE(provider, ''), created_at, is_summary_message
FROM messages
WHERE created_at >= ? AND created_at < ?
ORDER BY session_id, created_at, rowid`

// Run makes one pass over the messages in the window and returns the
// report. It reads row by row, because a single parts value can exceed
// half a megabyte, and only reads, so conn should be opened read-only.
func Run(ctx context.Context, conn *sql.DB, opts Options) (*Report, error) {
	since, until := int64(math.MinInt64), int64(math.MaxInt64)
	if !opts.Since.IsZero() {
		since = opts.Since.Unix()
	}
	if !opts.Until.IsZero() {
		until = opts.Until.Unix()
	}
	rows, err := conn.QueryContext(ctx, messagesQuery, since, until)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	p := newPass(opts)
	sections := newSections()
	current := ""
	for rows.Next() {
		var (
			m     message.Message
			role  string
			parts []byte
		)
		if err := rows.Scan(&m.SessionID, &role, &parts, &m.Model, &m.Provider, &m.CreatedAt, &m.IsSummaryMessage); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if p.sessions == 0 || m.SessionID != current {
			if p.sessions > 0 {
				p.endSession(sections)
			}
			current = m.SessionID
			p.sessions++
		}
		m.Role = message.MessageRole(role)
		p.count(&m)
		if m.Parts, err = message.DecodeParts(parts); err != nil {
			p.skipped++
			continue
		}
		p.observe(&m)
		for _, s := range sections {
			s.message(p, &m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	if p.sessions > 0 {
		p.endSession(sections)
	}

	r := &Report{Schema: Schema}
	for _, s := range sections {
		s.finish(p, r)
	}
	return r, nil
}

func newPass(opts Options) *pass {
	return &pass{
		opts:     opts,
		roles:    map[string]int{},
		calls:    map[string]call{},
		stamping: map[string]bool{},
	}
}

// count records every message, whether its parts decode or not.
func (p *pass) count(m *message.Message) {
	p.roles[string(m.Role)]++
	if p.first == 0 || m.CreatedAt < p.first {
		p.first = m.CreatedAt
	}
	p.last = max(p.last, m.CreatedAt)
}

func (p *pass) endSession(sections []section) {
	for _, s := range sections {
		s.endSession(p)
	}
	clear(p.calls)
}

// observe records the message's tool calls, and which builds stamp their
// results.
func (p *pass) observe(m *message.Message) {
	switch m.Role {
	case message.Assistant:
		build := ""
		if f := m.FinishPart(); f != nil {
			build = f.Build
		}
		for _, tc := range m.ToolCalls() {
			p.calls[tc.ID] = call{name: tc.Name, build: build}
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			c, ok := p.calls[tr.ToolCallID]
			if ok && c.build != "" && hasStamp(tr) {
				p.stamping[c.build] = true
			}
		}
	}
}

func hasStamp(tr message.ToolResult) bool {
	if gjson.Get(tr.Metadata, "error_kind").Exists() {
		return true
	}
	return tr.Name == bashTool && (gjson.Get(tr.Metadata, "exit_code").Exists() ||
		gjson.Get(tr.Metadata, "interrupted").Exists())
}

// kindName names a failure's kind for the report. A failure without one
// reads "other" when the build that made the call stamps kinds, and
// "unstamped" when it does not or is unknown.
func (p *pass) kindName(kind, build string) string {
	switch {
	case kind != "":
		return kind
	case build != "" && p.stamping[build]:
		return "other"
	default:
		return "unstamped"
	}
}

// Tool names the report reads: the names models call.
const (
	bashTool      = "bash"
	editTool      = "edit"
	multiEditTool = "multiedit"
	writeTool     = "write"
	viewTool      = "view"
	readTool      = "read"
)

func ratio(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}

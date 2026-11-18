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
}

// section is one part of the report. The pass shows it every message it
// could decode, in session_id, created_at, rowid order, and tells it when
// a session ends. Besides its counters a section keeps state for the
// current session only. finish writes its figures into the report once
// the pass is over.
type section interface {
	message(p *pass, m *message.Message)
	endSession(p *pass)
	finish(p *pass, r *Report)
}

// newSections returns the report's sections in the order they print.
func newSections() []section {
	return []section{&windowSection{builds: map[string]*Build{}}}
}

// pass holds what the sections share.
type pass struct {
	opts     Options
	roles    map[string]int
	sessions int
	skipped  int
	first    int64
	last     int64
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
	return &pass{opts: opts, roles: map[string]int{}}
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
}

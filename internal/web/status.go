package web

import (
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/wire"
)

// Session status values the sidebar renders. A session waiting for the
// person outranks one that is merely running: the first needs an answer,
// the second only needs time.
const (
	statusIdle               = "idle"
	statusRunning            = "running"
	statusQueued             = "queued"
	statusPendingInteraction = "pending_interaction"
)

// sessionStatus computes the live status of one session from the same
// sources the terminal reads, so a sidebar marker cannot disagree with what
// the TUI shows.
func (g *Gateway) sessionStatus(sessionID string) string {
	switch {
	case g.pending.interacts(sessionID):
		return statusPendingInteraction
	case g.ws.AgentIsSessionBusy(sessionID):
		return statusRunning
	case len(g.ws.AgentQueuedPromptsList(sessionID)) > 0:
		return statusQueued
	default:
		return statusIdle
	}
}

// sessionRow is one session as the sidebar lists it: the wire session
// entity with the live status beside it.
type sessionRow struct {
	proto.Session
	Status string `json:"status"`
}

// sortSessionsNewestFirst orders a session list the way the sidebar and the
// session switcher show it: newest updated first, the ID breaking ties so two
// sessions saved in the same second keep a stable order.
func sortSessionsNewestFirst(list []session.Session) {
	slices.SortFunc(list, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})
}

// sessionRows turns a session list into the rows the sidebar renders,
// keeping the caller's order.
func (g *Gateway) sessionRows(list []session.Session) []sessionRow {
	rows := make([]sessionRow, 0, len(list))
	for _, s := range list {
		rows = append(rows, sessionRow{
			Session: wire.SessionToProto(s),
			Status:  g.sessionStatus(s.ID),
		})
	}
	return rows
}

// seedSessionStatuses records every session's status without publishing, so
// the first event announces only what really changed. It is the gateway's
// one session list read for status; everything after it is per-session.
func (g *Gateway) seedSessionStatuses(ctx context.Context) {
	sessions, err := g.ws.ListSessions(ctx)
	if err != nil {
		slog.Debug("Web session status seed could not list sessions", "error", err)
		return
	}
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	for _, s := range sessions {
		g.statuses[s.ID] = g.sessionStatus(s.ID)
	}
}

// refreshSessionStatus recomputes one session's status and publishes a frame
// only when the value changed. The read and the publish happen under
// statusMu, so two refreshes cannot publish an older status after a newer
// one. The pump calls it for the session an event names, which is what keeps
// the gateway off a periodic poll.
func (g *Gateway) refreshSessionStatus(sessionID string) {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	status := g.sessionStatus(sessionID)
	if last, ok := g.statuses[sessionID]; ok && last == status {
		return
	}
	g.statuses[sessionID] = status
	g.hub.publishJSON(frameSessionStatus, map[string]string{
		"session_id": sessionID,
		"status":     status,
	})
}

// forgetSessionStatus drops a deleted session from the map, so a later
// session that reuses the id starts from a clean slate.
func (g *Gateway) forgetSessionStatus(sessionID string) {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	delete(g.statuses, sessionID)
}

// statusSessionID returns the session whose status ev can change: a
// permission or question arriving or resolving, a run ending, or a user
// prompt starting a run or joining the queue. Every other event answers "",
// including the streamed assistant chunks that arrive many times a second.
func statusSessionID(ev any) string {
	switch e := ev.(type) {
	case pubsub.Event[permission.PermissionRequest]:
		return e.Payload.SessionID
	case pubsub.Event[permission.PermissionNotification]:
		return e.Payload.SessionID
	case pubsub.Event[question.Request]:
		return e.Payload.SessionID
	case pubsub.Event[question.Notification]:
		return e.Payload.SessionID
	case pubsub.Event[notify.RunComplete]:
		return e.Payload.SessionID
	case pubsub.Event[message.Message]:
		if e.Payload.Role == message.User {
			return e.Payload.SessionID
		}
	}
	return ""
}

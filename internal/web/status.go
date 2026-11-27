package web

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

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

// refreshSessionStatus recomputes one session's status, publishes a frame
// only when the value changed, and returns the status it computed so a
// caller can tell what the session now is. The read and the publish happen
// under statusMu, so two refreshes cannot publish an older status after a
// newer one. The pump calls it for the session an event names, which is what
// keeps the gateway off a periodic poll.
func (g *Gateway) refreshSessionStatus(sessionID string) string {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	status := g.sessionStatus(sessionID)
	if last, ok := g.statuses[sessionID]; ok && last == status {
		return status
	}
	g.statuses[sessionID] = status
	g.hub.publishJSON(frameSessionStatus, map[string]string{
		"session_id": sessionID,
		"status":     status,
	})
	return status
}

// forgetSessionStatus drops a deleted session from the map, so a later
// session that reuses the id starts from a clean slate.
func (g *Gateway) forgetSessionStatus(sessionID string) {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	delete(g.statuses, sessionID)
}

// statusSessionID returns the session whose status ev can change: a
// permission or question arriving or resolving, a run ending, or a message
// starting a run or joining the queue. Every other event answers "",
// including the streamed chunks that arrive many times a second.
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
		// Any created message, whatever its role: a hidden wake-up turn,
		// a report turn and a gate continuation start without a user
		// prompt, so their first assistant message is the only sign the
		// session is running. Updates are the streamed chunks.
		if e.Type == pubsub.CreatedEvent {
			return e.Payload.SessionID
		}
	}
	return ""
}

// runEnded reports whether ev ends a run, the one event whose refresh can
// read a session that is already over.
func runEnded(ev any) bool {
	_, ok := ev.(pubsub.Event[notify.RunComplete])
	return ok
}

// statusSettleDelays are the waits before each re-check of a session whose
// run ended while the agent still reported it busy. The list is bounded, so
// a session that never settles stops costing anything after the last wait.
// Only a run end starts the chain, which keeps the gateway off a periodic
// sweep. Tests shorten the list.
var statusSettleDelays = []time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	time.Second,
}

// statusSettle is one pending re-check. seq tells the live timer from one a
// newer run end has replaced.
type statusSettle struct {
	seq   uint64
	timer *time.Timer
}

// settleSessionStatus starts the bounded re-check chain for a session a run
// end left running: the agent clears its busy flag after it publishes
// run_complete, so the refresh that event triggered can still read running
// and publish nothing. Without the chain that marker would stay stuck until
// some unrelated event happened to correct it.
func (g *Gateway) settleSessionStatus(sessionID string) {
	g.settleMu.Lock()
	defer g.settleMu.Unlock()
	g.scheduleStatusSettleLocked(sessionID, 0)
}

// scheduleStatusSettleLocked arms the re-check at index i, replacing any
// timer already armed for the session. The caller holds settleMu.
func (g *Gateway) scheduleStatusSettleLocked(sessionID string, i int) {
	if g.settleClosed {
		return
	}
	g.settleSeq++
	seq := g.settleSeq
	if entry, ok := g.settles[sessionID]; ok {
		entry.timer.Stop()
	}
	g.settles[sessionID] = &statusSettle{
		seq: seq,
		timer: time.AfterFunc(statusSettleDelays[i], func() {
			g.recheckSessionStatus(sessionID, seq, i)
		}),
	}
}

// recheckSessionStatus republishes one session's status and keeps the chain
// going while it still reads running. A timer a newer run end replaced, or
// one Shutdown cancelled, does nothing.
func (g *Gateway) recheckSessionStatus(sessionID string, seq uint64, i int) {
	g.settleMu.Lock()
	entry, ok := g.settles[sessionID]
	if !ok || entry.seq != seq || g.settleClosed {
		g.settleMu.Unlock()
		return
	}
	delete(g.settles, sessionID)
	g.settleMu.Unlock()

	if g.refreshSessionStatus(sessionID) != statusRunning {
		return
	}
	if i+1 >= len(statusSettleDelays) {
		return
	}
	g.settleMu.Lock()
	defer g.settleMu.Unlock()
	g.scheduleStatusSettleLocked(sessionID, i+1)
}

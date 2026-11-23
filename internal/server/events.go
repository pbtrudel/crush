package server

import (
	"context"
	"log/slog"

	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
)

// sessionScopedEventID extracts the session ID from events that
// belong to a single session's interactive prompts (permission and
// question requests and their resolution notifications). The second
// return value reports whether the event is session-scoped at all. An
// event whose session ID is empty is treated as unscoped: it cannot
// match any client's current session, so scoping it would drop it for
// everyone.
func sessionScopedEventID(ev any) (string, bool) {
	var id string
	switch e := ev.(type) {
	case pubsub.Event[permission.PermissionRequest]:
		id = e.Payload.SessionID
	case pubsub.Event[permission.PermissionNotification]:
		id = e.Payload.SessionID
	case pubsub.Event[question.Request]:
		id = e.Payload.SessionID
	case pubsub.Event[question.Notification]:
		id = e.Payload.SessionID
	default:
		return "", false
	}
	return id, id != ""
}

// deliverToClient reports whether a workspace event should be written
// to the given client's SSE stream. Session-scoped prompt events are
// only delivered to clients currently viewing the session that raised
// them: a permission dialog or question form is only actionable there,
// and broadcasting it to every client in the workspace lets sessions
// steal each other's prompts. Prompts raised by subagent sessions
// (agent tool, agentic fetch) resolve to the top-level session so the
// viewer of the parent session can answer them. Clients on the landing
// screen (an explicitly cleared session) receive no prompts, while
// clients that never reported a session at all (e.g. API clients
// predating session reporting) fail open so their prompts are not
// stranded. All other events are delivered unfiltered.
func (c *controllerV1) deliverToClient(ctx context.Context, workspaceID, clientID string, ev any) bool {
	sessionID, scoped := sessionScopedEventID(ev)
	if !scoped {
		return true
	}
	current, reported := c.backend.ClientCurrentSession(workspaceID, clientID)
	if !reported {
		// The client never reported a current session, so there is
		// nothing to scope against. Fail open rather than stranding a
		// prompt nobody can answer.
		return true
	}
	if current == "" {
		return false
	}
	if sessionID == current {
		return true
	}
	root, err := c.backend.RootSessionID(ctx, workspaceID, sessionID)
	if err != nil {
		// Fail open: an unresolvable session is delivered as before
		// rather than stranding a prompt nobody can answer.
		slog.Debug("Failed to resolve root session for event routing",
			"session_id", sessionID, "error", err)
		return true
	}
	return root == current
}

// isSessionBusy reports whether the given workspace has an in-flight
// agent run for sessionID. It tolerates a nil workspace (treating it as
// "not busy") so REST handlers can pass GetWorkspace's result through
// unconditionally — the workspace lookup error is already surfaced by
// the prior ListSessions/GetSession call when relevant.
func isSessionBusy(ws *backend.Workspace, sessionID string) bool {
	if ws == nil {
		return false
	}
	coord := ws.Coordinator()
	return coord != nil && coord.IsSessionBusy(sessionID)
}

// attachedClients returns the number of clients currently viewing
// sessionID in ws. Hold-only clients (streams == 0) do not contribute.
// A nil workspace is treated as zero so handlers can pass GetWorkspace's
// result through without an extra guard.
func attachedClients(ws *backend.Workspace, sessionID string) int {
	if ws == nil {
		return 0
	}
	return ws.AttachedClientsForSession(sessionID)
}

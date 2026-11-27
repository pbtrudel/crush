package web

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// maxTitleRunes is the longest title a rename accepts, matching the
// sessions dialog's limit.
const maxTitleRunes = 200

// registerSessionRoutes installs the session routes the browser uses to
// rename, delete, follow a session and run its automation commands. Like
// every other API route they register through g.route so the auth test
// walks them.
func (g *Gateway) registerSessionRoutes(api *http.ServeMux) {
	g.route(api, "PUT /api/sessions/{sid}", g.handleRenameSession)
	g.route(api, "DELETE /api/sessions/{sid}", g.handleDeleteSession)
	g.route(api, "POST /api/sessions/{sid}/automation", g.handleAutomation)
	g.route(api, "POST /api/follow", g.handleFollow)
}

// handleRenameSession changes only the title. It never saves the whole
// session, which could overwrite the usage, cost and todos a busy run
// wrote between the read and the write.
func (g *Gateway) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		writeError(w, http.StatusBadRequest, "bad_title", "title must be 1 to 200 characters")
		return
	}
	sess, err := g.ws.SessionRename(r.Context(), r.PathValue("sid"), title)
	if err != nil {
		if isSessionNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return
		}
		writeWorkspaceError(w, "rename_session", err)
		return
	}
	writeJSON(w, http.StatusOK, innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: sess}))
}

// handleDeleteSession checks in order: the session exists, no agent is
// working in it, and the terminal is not showing it. Only then does it
// delete, so a refusal always leaves the session alone.
func (g *Gateway) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	if g.ws.AgentIsSessionBusy(sid) {
		writeError(w, http.StatusConflict, "session_busy", "Crush is working in this session")
		return
	}
	if g.ws.CurrentSessionID() == sid {
		writeError(w, http.StatusConflict, "session_current", "The terminal is showing this session; switch it first")
		return
	}
	if err := g.ws.DeleteSession(r.Context(), sid); err != nil {
		if isSessionNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return
		}
		writeWorkspaceError(w, "delete_session", err)
		return
	}
	// A deleted session publishes no status frame: the session deleted
	// event already removes the row. Forgetting the cached status keeps a
	// later session that reuses the id from starting stale.
	g.forgetSessionStatus(sid)
	w.WriteHeader(http.StatusNoContent)
}

// handleFollow asks the terminal to open an existing session, the way the
// sessions dialog switches, so the tab follows through session_follow as
// for a browser-created session. Reporting the session is best effort,
// like handleCreateSession.
func (g *Gateway) handleFollow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sid := strings.TrimSpace(body.SessionID)
	if sid == "" {
		writeError(w, http.StatusBadRequest, "missing_session", "session_id is required")
		return
	}
	if !g.sessionExists(w, r, sid) {
		return
	}
	if err := g.ws.SetCurrentSession(r.Context(), sid); err != nil {
		slog.Debug("Failed to report the followed session", "session_id", sid, "error", err)
	}
	g.notifyOpenSession(sid)
	w.WriteHeader(http.StatusAccepted)
}

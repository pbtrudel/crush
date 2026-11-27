package web

import (
	"log/slog"
	"net/http"

	"github.com/charmbracelet/crush/internal/sessionexport"
)

// registerProjectRoutes installs the session export and project
// initialization routes. Like every other API route they register through
// g.route so the auth test walks them.
func (g *Gateway) registerProjectRoutes(api *http.ServeMux) {
	g.route(api, "POST /api/sessions/{sid}/export", g.handleExportSession)
	g.route(api, "POST /api/project/init", g.handleInitProject)
}

// handleExportSession writes the session to a Markdown file in the working
// directory and answers its path: the same file the terminal's "Export
// Session as Markdown" command writes, not a browser download.
func (g *Gateway) handleExportSession(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	path, err := sessionexport.WriteFile(r.Context(), g.ws, sid)
	if err != nil {
		if isSessionNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return
		}
		writeWorkspaceError(w, "export_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// handleInitProject starts the same project initialization the terminal's
// onboarding runs: a new session, the initialize prompt as its first
// message, and the project marked initialized. The prompt runs detached so
// the tab is not held for the whole turn, like a prompt sent from the
// composer.
func (g *Gateway) handleInitProject(w http.ResponseWriter, r *http.Request) {
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "init_project", err)
		return
	}
	if g.ws.AgentIsBusy() {
		writeError(w, http.StatusConflict, "agent_busy", "The agent is busy with a run; initialize the project once it finishes")
		return
	}
	prompt, err := g.ws.InitializePrompt()
	if err != nil {
		writeInternal(w, "init_prompt", err)
		return
	}
	s, err := g.ws.CreateSession(r.Context(), "New Session")
	if err != nil {
		writeWorkspaceError(w, "create_session", err)
		return
	}
	// Report the session before answering or notifying the terminal, so a
	// permission the prompt raises is routed to this client, the same
	// order handleCreateSession uses. Presence is best-effort.
	if err := g.ws.SetCurrentSession(r.Context(), s.ID); err != nil {
		slog.Debug("Failed to report the project session", "session_id", s.ID, "error", err)
	}
	g.notifyOpenSession(s.ID)
	writeJSON(w, http.StatusAccepted, map[string]string{"session_id": s.ID})
	g.runDetached(s.ID, prompt)
	// The flag is a local file write, and the prompt keeps running even
	// when it fails, as the terminal's onboarding does.
	if err := g.ws.MarkProjectInitialized(); err != nil {
		slog.Warn("Web project init could not mark the project initialized", "session_id", s.ID, "error", err)
	}
}

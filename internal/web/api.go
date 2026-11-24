package web

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
)

const (
	maxPromptBytes = 32 << 20
	maxJSONBytes   = 1 << 20
	maxCompletions = 50
)

// registerAPI installs the M1 REST API on api. Every handler assumes the
// caller is already authenticated.
func (g *Gateway) registerAPI(api *http.ServeMux) {
	g.route(api, "GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	g.route(api, "GET /api/stream", g.handleStream)
	g.route(api, "GET /api/snapshot", g.handleSnapshot)
	g.route(api, "GET /api/sessions", g.handleListSessions)
	g.route(api, "POST /api/sessions", g.handleCreateSession)
	g.route(api, "GET /api/sessions/{sid}", g.handleGetSession)
	g.registerSessionRoutes(api)
	g.route(api, "GET /api/sessions/{sid}/messages", g.handleMessages)
	g.route(api, "GET /api/sessions/{sid}/files", g.handleFiles)
	g.route(api, "GET /api/sessions/{sid}/files/diff", g.handleFileDiff)
	g.route(api, "POST /api/sessions/{sid}/prompt", g.handlePrompt)
	g.route(api, "POST /api/sessions/{sid}/cancel", g.handleCancel)
	g.route(api, "GET /api/sessions/{sid}/queue", g.handleQueue)
	g.route(api, "DELETE /api/sessions/{sid}/queue", g.handleClearQueue)
	g.route(api, "POST /api/permissions/grant", g.handleGrant)
	g.route(api, "POST /api/permissions/mode", g.handleMode)
	g.route(api, "POST /api/input-mode", g.handleInputMode)
	g.route(api, "POST /api/questions/answer", g.handleAnswer)
	g.route(api, "POST /api/questions/cancel", g.handleQuestionCancel)
	g.route(api, "GET /api/complete/files", g.handleCompleteFiles)
	g.route(api, "POST /api/team/members/{member}/message", g.handleTeamMessage)
	g.route(api, "POST /api/team/members/{member}/interrupt", g.handleTeamInterrupt)
	g.registerCommandRoutes(api)
	g.registerAgentRoutes(api)
	g.registerModelRoutes(api)
	g.registerSystemRoutes(api)
	g.registerPlanRoutes(api)
}

// route registers an API handler and records its pattern for the auth
// test. Every cookie-guarded /api route registers through here, the login
// route excepted: a direct api.HandleFunc would skip the registry, and
// with it the test that proves the route refuses a request without the
// cookie.
func (g *Gateway) route(api *http.ServeMux, pattern string, h http.HandlerFunc) {
	api.HandleFunc(pattern, h)
	g.apiRoutes = append(g.apiRoutes, pattern)
}

// writeInternal answers a failed workspace call without its error text,
// which can carry file paths, SQL or provider details, and logs it instead.
func writeInternal(w http.ResponseWriter, code string, err error) {
	slog.Warn("Web API call failed", "code", code, "error", err)
	writeError(w, http.StatusInternalServerError, code, "the workspace could not complete the request")
}

// isNotFound reports a lookup of something the workspace does not have. The
// in-process workspace answers sql.ErrNoRows; the server-backed one, which
// the gateway talks to in client/server mode, answers client.ErrNotFound
// for a workspace it no longer knows and client.ErrSessionNotFound for a
// session it does not have.
func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) ||
		errors.Is(err, client.ErrNotFound) ||
		errors.Is(err, client.ErrSessionNotFound)
}

// isSessionNotFound reports a session the workspace does not have. A
// workspace-gone 404 is not one: it says nothing about the session, and
// the subscription loop is already recovering the workspace, so session
// routes answer it as a workspace failure rather than "unknown session".
func isSessionNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, client.ErrSessionNotFound)
}

// decodeJSON reads a bounded JSON body into v. An empty body is accepted
// and leaves v untouched.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBytes)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return false
	}
	return true
}

func (g *Gateway) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = g.ws.CurrentSessionID()
	}
	writeJSON(w, http.StatusOK, g.buildSnapshot(r.Context(), sid))
}

func (g *Gateway) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := g.ws.ListSessions(r.Context())
	if err != nil {
		writeInternal(w, "list_sessions", err)
		return
	}
	// The switcher shows newest first. UpdatedAt alone can tie (two
	// sessions start within the same second), so the ID breaks ties and
	// keeps the order stable across requests.
	slices.SortFunc(list, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})
	writeJSON(w, http.StatusOK, entities(list))
}

func (g *Gateway) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		// Match the TUI, which creates "New Session" on the first prompt.
		title = "New Session"
	}
	s, err := g.ws.CreateSession(r.Context(), title)
	if err != nil {
		writeInternal(w, "create_session", err)
		return
	}
	// Report the session before answering or notifying the terminal, so a
	// permission the first prompt raises is routed to this client. The
	// terminal reports the same session again when it follows the callback,
	// which is harmless. Presence is best-effort, like the TUI's reports.
	if err := g.ws.SetCurrentSession(r.Context(), s.ID); err != nil {
		slog.Debug("Failed to report the browser-created session", "session_id", s.ID, "error", err)
	}
	// The terminal opens the session so both clients show the same
	// conversation. The callback is nil when no terminal is attached.
	g.notifyOpenSession(s.ID)
	writeJSON(w, http.StatusCreated, innerPayload(pubsub.Event[session.Session]{Type: pubsub.CreatedEvent, Payload: s}))
}

// handleInputMode asks the terminal to switch between plan and code mode.
// The gateway never switches the agent itself: the terminal owns the busy
// check and the agent switch.
func (g *Gateway) handleInputMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Mode != inputModePlan && body.Mode != inputModeCode {
		writeError(w, http.StatusBadRequest, "invalid_mode", "mode must be plan or code")
		return
	}
	g.notifyInputModeRequested(body.Mode == inputModePlan)
	w.WriteHeader(http.StatusAccepted)
}

func (g *Gateway) handleMessages(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if _, err := g.ws.GetSession(r.Context(), sid); err != nil {
		if isSessionNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return
		}
		writeInternal(w, "get_session", err)
		return
	}
	msgs, err := g.ws.ListMessages(r.Context(), sid)
	if err != nil {
		writeInternal(w, "list_messages", err)
		return
	}
	writeJSON(w, http.StatusOK, entities(msgs))
}

// handlePrompt starts a run detached from the HTTP request, the same way
// the TUI runs prompts in a background command. Failures reach the tab as
// a web_error frame.
func (g *Gateway) handlePrompt(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	r.Body = http.MaxBytesReader(w, r.Body, maxPromptBytes)
	if err := r.ParseMultipartForm(maxPromptBytes); err != nil {
		writeError(w, http.StatusBadRequest, "bad_form", "prompt must be multipart form data under 32 MiB")
		return
	}
	prompt := r.FormValue("prompt")
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "empty_prompt", "prompt is empty")
		return
	}
	var atts []message.Attachment
	for _, fh := range r.MultipartForm.File["attachments"] {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_attachment", "an attachment could not be read")
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_attachment", "an attachment could not be read")
			return
		}
		atts = append(atts, message.Attachment{
			FileName: filepath.Base(fh.Filename),
			MimeType: attachmentMime(data),
			Content:  data,
		})
	}
	g.runDetached(sid, prompt, atts...)
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

// runDetached starts text in sessionID without waiting for it, the way the
// TUI runs a prompt as a background command. The run outlives the request
// that started it, since a tab can navigate away while the agent works, and
// a failure reaches the tab as a web_error frame. The prompt, command and
// MCP prompt routes share it.
func (g *Gateway) runDetached(sessionID, text string, atts ...message.Attachment) {
	ctx := context.WithoutCancel(g.ctx)
	go func() {
		if err := g.ws.AgentRun(ctx, sessionID, text, atts...); err != nil {
			slog.Warn("Web prompt failed", "session_id", sessionID, "error", err)
			g.hub.publishJSON(frameError, promptFailure(sessionID, err))
		}
	}()
}

// attachmentMime classifies attachment content the way the TUI does: the
// first 512 bytes decide, and the file name is ignored. Browser-supplied
// types and extensions lie often enough (a .json holds text, a pasted PNG
// can keep any name) that the bytes are the only trustworthy signal.
func attachmentMime(data []byte) string {
	return http.DetectContentType(data[:min(512, len(data))])
}

func (g *Gateway) handleCancel(w http.ResponseWriter, r *http.Request) {
	g.ws.AgentCancel(r.PathValue("sid"))
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleQueue(w http.ResponseWriter, r *http.Request) {
	q := g.ws.AgentQueuedPromptsList(r.PathValue("sid"))
	if q == nil {
		q = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"queued": q})
}

func (g *Gateway) handleClearQueue(w http.ResponseWriter, r *http.Request) {
	g.ws.AgentClearQueue(r.PathValue("sid"))
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	req, ok := g.pending.permission(body.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_pending", "permission request is no longer pending")
		return
	}
	var resolved bool
	switch body.Action {
	case "allow":
		resolved = g.ws.PermissionGrant(req)
	case "allow_session":
		resolved = g.ws.PermissionGrantPersistent(req)
	case "deny":
		resolved = g.ws.PermissionDeny(req)
	default:
		writeError(w, http.StatusBadRequest, "bad_action", "action must be allow, allow_session or deny")
		return
	}
	if !resolved {
		// The service no longer holds the request, so it can never be
		// answered from the browser.
		g.pending.forget(body.ID)
		writeError(w, http.StatusNotFound, "not_pending", "permission request is no longer pending")
		return
	}
	g.pending.forget(body.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
}

func (g *Gateway) handleMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch proto.WorkspacePermissionMode(body.Mode) {
	case proto.WorkspacePermissionModeNormal, proto.WorkspacePermissionModeYolo, proto.WorkspacePermissionModeSysadmin:
	default:
		writeError(w, http.StatusBadRequest, "bad_mode", "mode must be normal, yolo or sysadmin")
		return
	}
	g.ws.PermissionSetMode(proto.ProtoModeToPermission(proto.WorkspacePermissionMode(body.Mode)))
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchID string            `json:"batch_id"`
		Answers []question.Answer `json:"answers"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.BatchID == "" {
		writeError(w, http.StatusBadRequest, "bad_batch_id", "batch_id is required")
		return
	}
	if _, ok := g.pending.question(body.BatchID); ok && g.ws.QuestionAnswer(body.BatchID, body.Answers) {
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
		return
	}
	g.pending.forgetQuestion(body.BatchID)
	writeError(w, http.StatusNotFound, "not_pending", "question batch is no longer pending")
}

func (g *Gateway) handleQuestionCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchID string `json:"batch_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.BatchID == "" {
		writeError(w, http.StatusBadRequest, "bad_batch_id", "batch_id is required")
		return
	}
	if _, ok := g.pending.question(body.BatchID); ok && g.ws.QuestionCancel(body.BatchID) {
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
		return
	}
	g.pending.forgetQuestion(body.BatchID)
	writeError(w, http.StatusNotFound, "not_pending", "question batch is no longer pending")
}

func (g *Gateway) handleCompleteFiles(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// Glob syntax in user input must not be interpreted.
	q = strings.NewReplacer("[", "", "]", "", "{", "", "}", "", "*", "", "?", "").Replace(q)
	pattern := "**/*"
	if q != "" {
		pattern = "**/*" + q + "*"
	}
	root := g.ws.WorkingDir()
	files, truncated, err := fsext.GlobGitignoreAwareCtx(r.Context(), pattern, root, maxCompletions)
	if err != nil {
		writeInternal(w, "complete_files", err)
		return
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if rel, err := filepath.Rel(root, f); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out, "truncated": truncated})
}

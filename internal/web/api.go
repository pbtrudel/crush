package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

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
	api.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/stream", g.handleStream)
	api.HandleFunc("GET /api/snapshot", g.handleSnapshot)
	api.HandleFunc("GET /api/sessions", g.handleListSessions)
	api.HandleFunc("POST /api/sessions", g.handleCreateSession)
	api.HandleFunc("GET /api/sessions/{sid}/messages", g.handleMessages)
	api.HandleFunc("POST /api/sessions/{sid}/prompt", g.handlePrompt)
	api.HandleFunc("POST /api/sessions/{sid}/cancel", g.handleCancel)
	api.HandleFunc("GET /api/sessions/{sid}/queue", g.handleQueue)
	api.HandleFunc("DELETE /api/sessions/{sid}/queue", g.handleClearQueue)
	api.HandleFunc("POST /api/permissions/grant", g.handleGrant)
	api.HandleFunc("POST /api/permissions/mode", g.handleMode)
	api.HandleFunc("POST /api/questions/answer", g.handleAnswer)
	api.HandleFunc("POST /api/questions/cancel", g.handleQuestionCancel)
	api.HandleFunc("GET /api/complete/files", g.handleCompleteFiles)
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
		writeError(w, http.StatusInternalServerError, "list_sessions", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entities(list))
}

func (g *Gateway) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s, err := g.ws.CreateSession(r.Context(), body.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_session", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, innerPayload(pubsub.Event[session.Session]{Type: pubsub.CreatedEvent, Payload: s}))
}

func (g *Gateway) handleMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := g.ws.ListMessages(r.Context(), r.PathValue("sid"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_messages", err.Error())
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
			writeError(w, http.StatusBadRequest, "bad_attachment", err.Error())
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_attachment", err.Error())
			return
		}
		atts = append(atts, message.Attachment{
			FileName: filepath.Base(fh.Filename),
			MimeType: attachmentMime(fh.Filename, data),
			Content:  data,
		})
	}
	ctx := context.WithoutCancel(g.ctx)
	go func() {
		if err := g.ws.AgentRun(ctx, sid, prompt, atts...); err != nil {
			slog.Warn("Web prompt failed", "session_id", sid, "error", err)
			g.hub.publishJSON(frameError, map[string]string{"session_id": sid, "error": err.Error()})
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func attachmentMime(name string, data []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return strings.Split(t, ";")[0]
	}
	return strings.Split(http.DetectContentType(data), ";")[0]
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
	if resolved {
		g.pending.forget(body.ID)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": resolved})
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
		Answers []question.Answer `json:"answers"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": g.ws.QuestionAnswer(body.Answers)})
}

func (g *Gateway) handleQuestionCancel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": g.ws.QuestionCancel()})
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
		writeError(w, http.StatusInternalServerError, "complete_files", err.Error())
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

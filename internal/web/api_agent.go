package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// maxAgentBodyBytes bounds the side question and the shell command,
	// so a pasted page cannot ride into a provider request or a shell.
	maxAgentBodyBytes = 16 << 10
	// maxNoteBytes bounds one note.
	maxNoteBytes = 64 << 10
	// shellChunkBytes caps one shell_output frame.
	shellChunkBytes = 16 << 10
	// shellFlushInterval coalesces progress writes into frames.
	shellFlushInterval = 100 * time.Millisecond
	// shellTermWidth matches the TUI's default for command output.
	shellTermWidth = 120
	// shellCancelledExitCode is the TUI's conventional SIGINT exit code.
	shellCancelledExitCode = 130
	// shellFailedText is what the browser shows for a failed command;
	// the detail stays in the terminal log.
	shellFailedText = "The command could not run. The terminal log has the details."
)

// shellOutputPayload is one streamed chunk of a shell command.
type shellOutputPayload struct {
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Chunk     string `json:"chunk"`
}

// shellDonePayload ends a shell stream. Error is set only for a failure
// that is not a cancellation, which reads as the TUI's SIGINT code.
type shellDonePayload struct {
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Done      bool   `json:"done"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error,omitempty"`
}

// registerAgentRoutes installs the agent action routes: compaction, side
// questions, shell commands and notes. Like every API route they register
// through g.route so the auth test walks them.
func (g *Gateway) registerAgentRoutes(api *http.ServeMux) {
	g.route(api, "POST /api/sessions/{sid}/summarize", g.handleSummarize)
	g.route(api, "POST /api/sessions/{sid}/ask", g.handleAsk)
	g.route(api, "POST /api/sessions/{sid}/shell", g.handleShell)
	g.route(api, "DELETE /api/shell/{run}", g.handleShellCancel)
	g.route(api, "POST /api/sessions/{sid}/notes", g.handleAddNote)
}

// handleSummarize compacts the session detached from the request, the way
// the TUI runs compaction as a background command. The TUI refuses while
// the session is busy, so the gateway does too.
func (g *Gateway) handleSummarize(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "summarize", err)
		return
	}
	if !g.sessionExists(w, r, sid) {
		return
	}
	if g.ws.AgentIsSessionBusy(sid) {
		writeError(w, http.StatusConflict, "agent_busy", "the session is busy")
		return
	}
	g.summarizeDetached(sid)
	w.WriteHeader(http.StatusAccepted)
}

// summarizeDetached runs compaction without waiting for it. A tab can
// navigate away while it runs, and a failure reaches the tab as a
// web_error frame. It is a sibling of runDetached rather than a shared
// helper: the two differ only in the workspace call and the failure
// payload, so a parameterised runner would add indirection without reuse.
func (g *Gateway) summarizeDetached(sessionID string) {
	ctx := context.WithoutCancel(g.ctx)
	go func() {
		if err := g.ws.AgentSummarize(ctx, sessionID); err != nil {
			slog.Warn("Web session summarization failed", "session_id", sessionID, "error", err)
			g.hub.publishJSON(frameError, summarizeFailure(sessionID, err))
		}
	}()
}

func (g *Gateway) handleAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Question string `json:"question"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "agent_ask", err)
		return
	}
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	switch {
	case strings.TrimSpace(body.Question) == "":
		writeError(w, http.StatusBadRequest, "empty_question", "question is empty")
		return
	case len(body.Question) > maxAgentBodyBytes:
		writeError(w, http.StatusBadRequest, "question_too_large", "question is over 16 KiB")
		return
	}
	answer, err := g.ws.AgentAsk(r.Context(), sid, body.Question)
	if err != nil {
		writeWorkspaceError(w, "agent_ask", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"answer": answer})
}

func (g *Gateway) handleAddNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "add_note", err)
		return
	}
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	// The in-process workspace silently ignores an empty note while the
	// client/server one answers 400, so the gateway decides for both.
	switch {
	case strings.TrimSpace(body.Text) == "":
		writeError(w, http.StatusBadRequest, "empty_note", "note is empty")
		return
	case len(body.Text) > maxNoteBytes:
		writeError(w, http.StatusBadRequest, "note_too_large", "note is over 64 KiB")
		return
	}
	if err := g.ws.AgentAddNote(r.Context(), sid, body.Text); err != nil {
		writeWorkspaceError(w, "add_note", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleShell(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string `json:"command"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "run_shell", err)
		return
	}
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	switch {
	case strings.TrimSpace(body.Command) == "":
		writeError(w, http.StatusBadRequest, "empty_command", "command is empty")
		return
	case len(body.Command) > maxAgentBodyBytes:
		writeError(w, http.StatusBadRequest, "command_too_large", "command is over 16 KiB")
		return
	}
	// The TUI marks a command the session's first message so the workspace
	// generates a title from it.
	msgs, err := g.ws.ListMessages(r.Context(), sid)
	if err != nil {
		writeWorkspaceError(w, "run_shell", err)
		return
	}
	id, err := newShellRunID()
	if err != nil {
		writeInternal(w, "run_shell", err)
		return
	}
	if !g.startShell(id, sid, body.Command, len(msgs) == 0) {
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": id})
}

// startShell registers a run and starts it. It reports false once the
// gateway is shutting down, so a late request cannot start work whose
// frames would arrive after the hub closed.
func (g *Gateway) startShell(id, sessionID, command string, isFirst bool) bool {
	g.shellMu.Lock()
	defer g.shellMu.Unlock()
	if g.shellsClosed {
		return false
	}
	ctx, cancel := context.WithCancel(g.ctx)
	g.shells[id] = cancel
	g.shellWG.Add(1)
	go g.runShell(ctx, cancel, id, sessionID, command, isFirst)
	return true
}

// runShell streams one shell command's output as coalesced frames and
// publishes the final frame after the last flush. The workspace persists
// the output itself, so the gateway persists nothing.
func (g *Gateway) runShell(ctx context.Context, cancel context.CancelFunc, id, sessionID, command string, isFirst bool) {
	defer g.shellWG.Done()
	// The run owns its cancel func, so a command that finishes on its own
	// releases the context instead of leaving it for the gateway's exit.
	defer cancel()
	defer func() {
		g.shellMu.Lock()
		delete(g.shells, id)
		g.shellMu.Unlock()
	}()

	var mu sync.Mutex
	var pending strings.Builder
	onProgress := func(chunk string) {
		mu.Lock()
		pending.WriteString(chunk)
		mu.Unlock()
	}
	flush := func() {
		mu.Lock()
		data := pending.String()
		pending.Reset()
		mu.Unlock()
		for len(data) > 0 {
			n := min(len(data), shellChunkBytes)
			if n < len(data) {
				for n > 0 && !utf8.RuneStart(data[n]) {
					n--
				}
				if n == 0 {
					n = min(len(data), shellChunkBytes)
				}
			}
			g.hub.publishJSON(frameShellOutput, shellOutputPayload{
				RunID: id, SessionID: sessionID, Chunk: data[:n],
			})
			data = data[n:]
		}
	}

	stop := make(chan struct{})
	var ticker sync.WaitGroup
	ticker.Add(1)
	tick := g.shellTick
	go func() {
		defer ticker.Done()
		if tick == nil {
			t := time.NewTicker(shellFlushInterval)
			defer t.Stop()
			tick = t.C
		}
		for {
			select {
			case <-tick:
				flush()
			case <-stop:
				return
			}
		}
	}()
	resp, err := g.ws.AgentRunShellCommand(ctx, sessionID, command, shellTermWidth, onProgress, isFirst)
	close(stop)
	ticker.Wait()
	flush()

	done := shellDonePayload{
		RunID: id, SessionID: sessionID, Done: true, ExitCode: resp.ExitCode,
	}
	// The real streaming path reports a cancelled run as a nil error and
	// a non-zero exit code, so a finished context decides before the
	// error does: that run reads as the TUI's SIGINT convention.
	switch {
	case ctx.Err() != nil, errors.Is(err, context.Canceled):
		done.ExitCode = shellCancelledExitCode
	case err != nil:
		slog.Warn("Web shell command failed", "session_id", sessionID, "error", err)
		done.Error = shellFailedText
	}
	g.hub.publishJSON(frameShellOutput, done)
}

func (g *Gateway) handleShellCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	g.shellMu.Lock()
	cancel, ok := g.shells[id]
	if ok {
		delete(g.shells, id)
	}
	g.shellMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_run", "no such run")
		return
	}
	cancel()
	w.WriteHeader(http.StatusNoContent)
}

func newShellRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// automationTimeout bounds how long an automation command waits for
	// the terminal's reply. The terminal answers at once unless its event
	// loop is stuck.
	automationTimeout = 10 * time.Second
	// maxAutomationArgs bounds the argument text a command accepts.
	maxAutomationArgs = 4096
	// automationFailureText is what the browser shows when the terminal's
	// reply carries an error. The detail stays in the log, like every
	// other workspace failure.
	automationFailureText = "The command could not run. The terminal log has the details."
)

// handleAutomation runs a /goal or /loop command in the terminal, which
// owns the automation state and its scheduler, and answers its reply. The
// four palette items (goal status, clear goal, loop status, clear loops)
// are this route with "status" or "clear" as the args.
func (g *Gateway) handleAutomation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Args string `json:"args"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name != "goal" && body.Name != "loop" {
		writeError(w, http.StatusBadRequest, "bad_name", "name must be goal or loop")
		return
	}
	if utf8.RuneCountInString(body.Args) > maxAutomationArgs {
		writeError(w, http.StatusBadRequest, "args_too_long", "args must be at most 4096 characters")
		return
	}
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	fn := g.automationCallback()
	if fn == nil {
		writeError(w, http.StatusServiceUnavailable, "no_terminal", "no terminal is attached")
		return
	}
	timeout := g.automationTimeout
	if timeout <= 0 {
		timeout = automationTimeout
	}
	// The deadline covers the terminal's answer, not a turn the command
	// starts, which runs in the TUI after the reply.
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	reply, err := fn(ctx, sid, body.Name, body.Args)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			// The browser left before the terminal answered. Nobody reads
			// the answer, and a cancel is not a workspace failure, so it
			// must not reach the log as a 500.
			return
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "terminal_timeout", "the terminal did not answer in time")
			return
		default:
			writeInternal(w, "automation_failed", err)
			return
		}
	}
	// The terminal prefixes a failed command with "Error: ", which the
	// browser must not show verbatim: it can carry paths and sockets. An
	// empty reply reads as handled, the way the Discord bridge answers.
	if strings.HasPrefix(reply, "Error: ") {
		slog.Warn("Web automation command failed", "session_id", sid, "name", body.Name, "error", strings.TrimPrefix(reply, "Error: "))
		reply = automationFailureText
	} else if strings.TrimSpace(reply) == "" {
		reply = "/" + body.Name + " handled."
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

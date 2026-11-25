package web

import (
	"context"
	"errors"
	"net/http"
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
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "terminal_timeout", "the terminal did not answer in time")
			return
		}
		writeInternal(w, "automation_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

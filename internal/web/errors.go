package web

import (
	"errors"
	"net/http"

	"github.com/charmbracelet/crush/internal/workspace"
)

// agentStartingText is what the browser shows while the coder agent is not
// ready yet: no model chosen, or the coordinator being rebuilt after a
// config reload.
const agentStartingText = "Crush is starting the agent"

// reconnectingText is what the browser shows while the client/server link
// is lost. Reconnecting is a state the caller can wait out, not a failure
// of the request.
const reconnectingText = "Crush is reconnecting to its server. Try again in a moment."

// writeWorkspaceError answers a failed workspace call. A state the caller
// can wait out is a 503 with a code the browser branches on: an agent that
// is not ready yet reads as "starting", and a lost link reads as
// "reconnecting", each with a fixed sentence. Anything else is a 500
// without the error text.
func writeWorkspaceError(w http.ResponseWriter, code string, err error) {
	switch {
	case errors.Is(err, workspace.ErrAgentNotInitialized):
		writeError(w, http.StatusServiceUnavailable, "agent_starting", agentStartingText)
		return
	case errors.Is(err, workspace.ErrWorkspaceGone):
		writeError(w, http.StatusServiceUnavailable, "workspace_gone", reconnectingText)
		return
	case errors.Is(err, workspace.ErrServerUnreachable):
		writeError(w, http.StatusServiceUnavailable, "server_unreachable", reconnectingText)
		return
	}
	writeInternal(w, code, err)
}

// webErrorPayload is the payload of a web_error frame: the session it
// concerns, a machine code the browser branches on, and a message safe to
// show.
func webErrorPayload(sessionID, code, message string) map[string]string {
	return map[string]string{"session_id": sessionID, "code": code, "error": message}
}

// promptFailure is the web_error payload for a detached prompt that could
// not run. The message is fixed, never the workspace error, which can name
// files or sockets; the log keeps the detail.
func promptFailure(sessionID string, err error) map[string]string {
	if errors.Is(err, workspace.ErrAgentNotInitialized) {
		return webErrorPayload(sessionID, "agent_starting", agentStartingText+"; send the prompt again in a moment")
	}
	return webErrorPayload(sessionID, "prompt_failed", "The prompt could not run. The terminal log has the details.")
}

// summarizeFailure is the web_error payload for a detached compaction that
// could not run. A not-ready agent reuses the prompt payload, since the
// browser shows the same starting state; anything else keeps the fixed
// message, never the workspace error.
func summarizeFailure(sessionID string, err error) map[string]string {
	if errors.Is(err, workspace.ErrAgentNotInitialized) {
		return promptFailure(sessionID, err)
	}
	return webErrorPayload(sessionID, "summarize_failed", "The session could not be compacted.")
}

// mcpAuthFailure is the web_error payload for an MCP OAuth flow that
// failed for a reason other than cancellation. The message is fixed, never
// the workspace error; the log keeps the detail. No session is named
// because the flow belongs to no session.
func mcpAuthFailure() map[string]string {
	return webErrorPayload("", "mcp_auth_failed", "The MCP server could not be authorised. The terminal log has the details.")
}

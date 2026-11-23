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

// writeWorkspaceError answers a failed workspace call. An agent that is not
// ready yet is a state the caller can wait out, so it is 503 with a code
// the browser shows as "starting"; anything else is a 500 without the error
// text.
func writeWorkspaceError(w http.ResponseWriter, code string, err error) {
	if errors.Is(err, workspace.ErrAgentNotInitialized) {
		writeError(w, http.StatusServiceUnavailable, "agent_starting", agentStartingText)
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

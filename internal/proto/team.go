package proto

// TeamCodeDisabled is the wire code a team action failure carries when
// Agent Teams is off. Clients map it back to agent.ErrAgentTeamsDisabled.
const TeamCodeDisabled = "teams_disabled"

// TeamCodeAgentNotInitialized is the wire code a team action failure
// carries when the workspace has no agent coordinator. Clients map it back
// to workspace.ErrAgentNotInitialized.
const TeamCodeAgentNotInitialized = ErrorCodeAgentNotInitialized

// TeamMessageRequest sends a message to a teammate as the Lead of a
// session's Agent Team.
type TeamMessageRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// TeamInterruptRequest stops a teammate's current turn.
type TeamInterruptRequest struct {
	SessionID string `json:"session_id"`
}

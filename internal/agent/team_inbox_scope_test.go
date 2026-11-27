package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

// Team tool calls deliver the caller's inbox, and every Lead session answers
// to "lead": a call made by one session's Lead must not read the mail a
// teammate of another session wrote for its own Lead.
func TestTeamToolsDoNotDeliverAnotherSessionsMail(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	runTeamTool(t, toolByName(leadTools, SpawnTeammateToolName), map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
	})

	teammateTools := testTeamTools(c, "reviewer")
	runTeamTool(t, toolByName(teammateTools, SendMessageToolName), map[string]any{
		"target":  "lead",
		"message": "Found a bug.",
	})

	other := context.WithValue(t.Context(), tools.SessionIDContextKey, "session-other")
	listed := runTeamToolIn(t, other, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.NotContains(t, listed.Content, "Found a bug.", "another session's Lead read this session's mail")
	require.Equal(t, 1, c.teamService().Tree(teamTestSessionID).UnreadByLead, "the report still waits for its Lead")

	owned := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.Contains(t, owned, "Message from reviewer: Found a bug.")
	require.Zero(t, c.teamService().Tree(teamTestSessionID).UnreadByLead, "the Lead read the report")
}

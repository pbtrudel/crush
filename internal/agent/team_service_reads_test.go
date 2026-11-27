package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/stretchr/testify/require"
)

// TestTeamResponseDrainsTheServiceItWasGiven pins that a tool response
// drains the inbox of the service the call ran on: a reload that retires
// the coordinator's pointer must not lose the mail the turn is answering.
func TestTeamResponseDrainsTheServiceItWasGiven(t *testing.T) {
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

	service := c.teamService()
	require.NotNil(t, service)
	// A reload retires the pointer while the turn still holds its service.
	c.setTeam(nil)

	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, teamTestSessionID)
	response := c.teamResponse(ctx, service, "lead", map[string]bool{"sent": true})
	require.Contains(t, response.Content, "Message from reviewer: Found a bug.")
	require.Zero(t, service.Tree(teamTestSessionID).UnreadByLead, "the response read the mailbox")
}

// TestTeammateSessionIDUsesTheRunnersService pins that a runner keeps its
// team id after a reload retires the coordinator's pointer: an id without
// it could collide with a session an earlier team left behind.
func TestTeammateSessionIDUsesTheRunnersService(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	service := c.teamService()
	require.NotNil(t, service)
	runner := &teamRunner{c: c, team: service}

	// A reload retires the coordinator's pointer mid-turn.
	c.setTeam(nil)

	id := runner.teammateSessionID(agentteam.Member{ID: "member-1"})
	require.Equal(t, "team-member-1-"+strings.TrimPrefix(service.TeamID(), "team-"), id)
	require.NotEqual(t, "team-member-1", id)
}

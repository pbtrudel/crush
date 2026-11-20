package agent

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// requireDelegationRefused runs a delegation tool and requires the refusal
// Agent Teams being off has to produce.
func requireDelegationRefused(t *testing.T, tool fantasy.AgentTool, input map[string]any) {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	response, err := tool.Run(t.Context(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  tool.Info().Name,
		Input: string(data),
	})
	require.NoError(t, err)
	require.True(t, response.IsError, "the tool must refuse with an error result")
	require.Contains(t, response.Content, "Agents are turned off")
}

// A turn that started with Agent Teams on holds a tool list that cannot be
// rebuilt under it, so the delegation tools have to refuse when the setting
// is off at call time.
func TestDelegationToolsRefuseWhenAgentTeamsTurnedOff(t *testing.T) {
	off := false
	c, _ := newOverrideTestCoordinator(t)
	c.cfg.Config().Options.AgentTeams = &off
	parentModel := func() Model { return c.mainModels().Large }

	dispatch, err := c.agentDispatchTool(t.Context(), config.AgentCoder, parentModel)
	require.NoError(t, err)
	requireDelegationRefused(t, dispatch, map[string]any{"prompt": "inspect", "label": "review"})

	requireDelegationRefused(t, c.agentSendTool(parentModel), map[string]any{"label": "review", "message": "go on"})

	requireDelegationRefused(t, c.spawnTeammateTool("lead", parentModel), map[string]any{
		"name": "reviewer", "description": "Reviews the diff", "prompt": "Review the diff.",
	})
}

// With Agent Teams on the same tools get past the setting and reach their
// own validation, which is what the refusal must not preempt.
func TestDelegationToolsRunWhenAgentTeamsOn(t *testing.T) {
	c, _ := newOverrideTestCoordinator(t)
	parentModel := func() Model { return c.mainModels().Large }

	send := c.agentSendTool(parentModel)
	data, err := json.Marshal(map[string]any{"label": "review", "message": "go on"})
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, "session-1")
	response, err := send.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: AgentSendToolName, Input: string(data)})
	require.NoError(t, err)
	require.True(t, response.IsError, "an unknown subagent is still an error")
	require.NotContains(t, response.Content, "Agents are turned off")
}

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

// With Agent Teams off, a turn that started while it was on can still call
// the Team tools. The ones that create work refuse; the ones that watch, wait
// on and stop work already in flight keep running, so nothing is stranded.
func TestTeamToolsRefuseOnlyTheWorkTheyCreateWhenAgentsTurnedOff(t *testing.T) {
	c, _ := newTeamTestCoordinator(t)
	off := false
	c.cfg.Config().Options.AgentTeams = &off
	held := testTeamTools(c, "lead")

	for name, input := range map[string]map[string]any{
		SendMessageToolName:    {"target": "lead", "message": "hello"},
		InterruptAgentToolName: {"target": "reviewer"},
		TeamTaskCreateToolName: {"subject": "review", "description": "Review the diff."},
	} {
		response := runTeamToolRaw(t, toolByName(held, name), input)
		require.True(t, response.IsError, name)
		require.Contains(t, response.Content, "Agents are turned off", name)
	}

	// Watching the board and waiting on it still works, or a turn would
	// have no way to see the work it cannot abandon.
	require.NotContains(t, runTeamTool(t, toolByName(held, TeamTaskListToolName), map[string]any{}), "Agents are turned off")
	require.NotContains(t, runTeamTool(t, toolByName(held, WaitAgentToolName), map[string]any{}), "Agents are turned off")
	for _, response := range []fantasy.ToolResponse{
		runTeamToolRaw(t, toolByName(held, TeamTaskGetToolName), map[string]any{"task_id": "no-such-task"}),
		runTeamToolRaw(t, toolByName(held, ListAgentsToolName), map[string]any{}),
	} {
		require.NotContains(t, response.Content, "Agents are turned off")
	}
}

// The status and stop tools are how a turn watches and stops the subagents it
// already started, so they keep running with Agent Teams off.
func TestStatusAndStopToolsRunWhenAgentsTurnedOff(t *testing.T) {
	off := false
	c, _ := newOverrideTestCoordinator(t)
	c.cfg.Config().Options.AgentTeams = &off
	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, "session-1")

	for name, tool := range map[string]fantasy.AgentTool{
		AgentStatusToolName: c.agentStatusTool(),
		AgentStopToolName:   c.agentStopTool(),
	} {
		data, err := json.Marshal(map[string]any{"label": "review"})
		require.NoError(t, err)
		response, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: name, Input: string(data)})
		require.NoError(t, err)
		require.NotContains(t, response.Content, "Agents are turned off", name)
	}
}

// The blocking agent tool outlives the setting in a headless run, so it has
// to refuse at call time too.
func TestLegacyAgentToolRefusesWhenAgentTeamsTurnedOff(t *testing.T) {
	off := false
	c, _ := newOverrideTestCoordinator(t)
	c.cfg.Config().Options.AgentTeams = &off

	agent, err := c.agentTool(t.Context(), config.AgentCoder)
	require.NoError(t, err)
	requireDelegationRefused(t, agent, map[string]any{"prompt": "inspect the diff"})
}

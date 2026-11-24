package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// newCrossFamilyCoordinator wires a coordinator that owns both a Team roster
// and a dispatch registry, so the two families of tools can be exercised
// against each other.
func newCrossFamilyCoordinator(t *testing.T) (*coordinator, fakeEnv, *teamFakeRunner) {
	t.Helper()
	c, env := newOverrideTestCoordinator(t)
	runner := newTeamFakeRunner()
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, runner)
	require.NoError(t, err)
	c.team = service
	return c, env, runner
}

// crossFamilyContext returns a Lead call context for a fresh parent session.
func crossFamilyContext(t *testing.T, env fakeEnv) (context.Context, string) {
	t.Helper()
	parent, err := env.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	return context.WithValue(t.Context(), tools.SessionIDContextKey, parent.ID), parent.ID
}

func spawnCrossFamilyTeammate(t *testing.T, c *coordinator, ctx context.Context) {
	t.Helper()
	spawn := toolByName(testTeamTools(c, "lead"), SpawnTeammateToolName)
	spawned := runTeamToolIn(t, ctx, spawn, map[string]any{
		"name":        "w-impl",
		"description": "Implements the change",
		"prompt":      "Do the work.",
	})
	require.False(t, spawned.IsError, spawned.Content)
}

// TestAgentStatusFindsTeammateByName pins that a teammate name answers
// agent_status from the roster instead of failing.
func TestAgentStatusFindsTeammateByName(t *testing.T) {
	t.Parallel()

	c, env, _ := newCrossFamilyCoordinator(t)
	ctx, _ := crossFamilyContext(t, env)
	spawnCrossFamilyTeammate(t, c, ctx)

	status := c.agentStatusTool()
	resp, err := status.Run(ctx, fantasy.ToolCall{ID: "s1", Name: AgentStatusToolName, Input: `{"label":"w-impl"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, `"target":"w-impl"`)
	require.Contains(t, resp.Content, `"status":"running"`)
}

// TestAgentStatusMissListsBothRosters pins that an unknown name still names
// the subagents and the teammates that exist.
func TestAgentStatusMissListsBothRosters(t *testing.T) {
	t.Parallel()

	c, env, _ := newCrossFamilyCoordinator(t)
	ctx, _ := crossFamilyContext(t, env)
	spawnCrossFamilyTeammate(t, c, ctx)

	status := c.agentStatusTool()
	resp, err := status.Run(ctx, fantasy.ToolCall{ID: "s1", Name: AgentStatusToolName, Input: `{"label":"ghost"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, `"ghost"`)
	require.Contains(t, resp.Content, "subagents:")
	require.Contains(t, resp.Content, "teammates:")
	require.Contains(t, resp.Content, "w-impl")
}

// TestAgentSendDeliversToTeammate pins that a teammate name sends through the
// Team mailbox.
func TestAgentSendDeliversToTeammate(t *testing.T) {
	t.Parallel()

	c, env, _ := newCrossFamilyCoordinator(t)
	ctx, parentID := crossFamilyContext(t, env)
	spawnCrossFamilyTeammate(t, c, ctx)

	send := c.agentSendTool(func() Model { return c.mainModels().Large })
	resp, err := send.Run(ctx, fantasy.ToolCall{
		ID:    "s1",
		Name:  AgentSendToolName,
		Input: `{"label":"w-impl","message":"status?"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Delivered to teammate")

	inbox := c.team.DrainInbox("w-impl", parentID)
	require.Len(t, inbox, 1)
	require.Equal(t, "status?", inbox[0].Text)
}

// TestAgentStopInterruptsTeammate pins that stopping a teammate name calls the
// team's interrupt.
func TestAgentStopInterruptsTeammate(t *testing.T) {
	t.Parallel()

	c, env, runner := newCrossFamilyCoordinator(t)
	ctx, _ := crossFamilyContext(t, env)
	spawnCrossFamilyTeammate(t, c, ctx)

	stop := c.agentStopTool()
	resp, err := stop.Run(ctx, fantasy.ToolCall{ID: "s1", Name: AgentStopToolName, Input: `{"label":"w-impl"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Interrupted teammate")

	runner.mu.Lock()
	run := runner.runs["w-impl"]
	runner.mu.Unlock()
	require.NotNil(t, run)
	select {
	case <-run.done:
	default:
		t.Fatal("the teammate's run was not interrupted")
	}
}

// TestSendMessageReachesDispatchedSubagent pins that send_message delivers to
// a dispatched subagent when the name is not a teammate.
func TestSendMessageReachesDispatchedSubagent(t *testing.T) {
	t.Parallel()

	c, env, _ := newCrossFamilyCoordinator(t)
	ctx := t.Context()
	agents, err := c.taskAgents(ctx, false)
	require.NoError(t, err)
	dispatch, err := c.agentDispatchTool(ctx, config.AgentCoder, agents[accessRead].Model)
	require.NoError(t, err)

	parent, err := env.sessions.Create(ctx, "parent")
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: "dispatching"}},
	})
	require.NoError(t, err)
	callCtx := context.WithValue(ctx, tools.SessionIDContextKey, parent.ID)
	callCtx = context.WithValue(callCtx, tools.MessageIDContextKey, assistant.ID)

	resp, err := dispatch.Run(callCtx, fantasy.ToolCall{ID: "d1", Name: AgentDispatchToolName, Input: `{"prompt":"do","label":"sub"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	var meta AgentDispatchResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	entry, ok := c.dispatched.Get(dispatchKey(parent.ID, meta.Label))
	require.True(t, ok)
	entry.stop("test done")
	require.True(t, entry.awaitSettled(10*time.Second))

	sendMessage := toolByName(testTeamTools(c, "lead"), SendMessageToolName)
	delivered := runTeamToolIn(t, callCtx, sendMessage, map[string]any{
		"target":  meta.Label,
		"message": "carry on",
	})
	require.False(t, delivered.IsError, delivered.Content)
	require.Contains(t, delivered.Content, "Delivered to")
}

// TestReopenedDispatchRunsCleanupAgain pins that the folder a subagent owns
// is removed after every run it performs, including each reopen that brings
// the folder back.
func TestReopenedDispatchRunsCleanupAgain(t *testing.T) {
	t.Parallel()

	c, env := newOverrideTestCoordinator(t)
	parent, err := env.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	// The test releases each run itself, so the result never depends on how
	// fast a real model run finishes on a loaded machine.
	release := make(chan struct{}, 2)
	agent := newMockAgent("mock", 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		<-release
		return agentResultWithText("done"), nil
	})
	entry := newDispatchedAgent("betty-cleanup", accessRead, "child", parent.ID, agent)

	var cleans atomic.Int64
	entry.setCleanup(func() { cleans.Add(1) })

	for want := int64(1); want <= 2; want++ {
		require.True(t, c.reopen(entry, "carry on", nil), "the idle entry reopens")
		// Stopped rather than finished, so no report turn starts on the parent.
		entry.stop("test done")
		release <- struct{}{}
		require.True(t, entry.awaitSettled(5*time.Second))
		require.Equal(t, want, cleans.Load(), "every run cleans up after itself")
	}
}

// TestAgentStopLeavesTeamAloneWhenAgentsOff pins that stopping a teammate
// name is refused, not forwarded, while Agent Teams is turned off.
func TestAgentStopLeavesTeamAloneWhenAgentsOff(t *testing.T) {
	t.Parallel()

	c, env, runner := newCrossFamilyCoordinator(t)
	ctx, _ := crossFamilyContext(t, env)
	spawnCrossFamilyTeammate(t, c, ctx)

	off := false
	c.cfg.Config().Options.AgentTeams = &off

	stop := c.agentStopTool()
	resp, err := stop.Run(ctx, fantasy.ToolCall{ID: "s1", Name: AgentStopToolName, Input: `{"label":"w-impl"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.NotContains(t, resp.Content, "Interrupted teammate", "an off team must not be asked to interrupt")

	runner.mu.Lock()
	run := runner.runs["w-impl"]
	runner.mu.Unlock()
	require.NotNil(t, run)
	select {
	case <-run.done:
		t.Fatal("a teammate run was interrupted while Agent Teams is off")
	default:
	}
}

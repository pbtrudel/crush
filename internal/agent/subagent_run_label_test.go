package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// TestDispatchedRunRegistersItsDispatchLabel pins that the run registry,
// which the web UI names agents from, uses the label a dispatch was given
// rather than the truncated first line of its prompt.
func TestDispatchedRunRegistersItsDispatchLabel(t *testing.T) {
	t.Parallel()

	c, env, _ := newCrossFamilyCoordinator(t)
	path := filepath.Join(t.TempDir(), "agents.json")
	c.agentRuns = agentrun.New(path)

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

	input, err := json.Marshal(AgentDispatchParams{
		Prompt: "Read /home/pbtrudel/git/crush/AGENTS.md and then sweep the HUD pin",
		Label:  "sweep-hud-pin",
	})
	require.NoError(t, err)
	resp, err := dispatch.Run(callCtx, fantasy.ToolCall{ID: "d1", Name: AgentDispatchToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	var meta AgentDispatchResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Contains(t, meta.Label, "sweep-hud-pin")

	entry, ok := c.dispatched.Get(dispatchKey(parent.ID, meta.Label))
	require.True(t, ok)

	var run agentrun.Run
	require.Eventually(t, func() bool {
		for _, r := range agentrun.Load(path) {
			if r.ID == entry.SessionID {
				run = r
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, meta.Label, run.Name)
	require.NotContains(t, run.Name, "Read /home/p")

	// Let the detached run settle and write its report, so nothing still
	// touches the test database once the test returns.
	entry.stop("test done")
	require.True(t, entry.awaitSettled(10*time.Second))
	require.Eventually(t, func() bool {
		msgs, err := env.messages.List(context.Background(), parent.ID)
		if err != nil {
			return false
		}
		for _, m := range msgs {
			if len(m.SubAgentReports()) > 0 {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)
}

// TestSubAgentRunFallsBackToPromptForItsRegistryName pins the fallback for
// callers that carry no label: the task tool keeps the prompt-derived name
// the sidebar has always shown.
func TestSubAgentRunFallsBackToPromptForItsRegistryName(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	path := filepath.Join(t.TempDir(), "agents.json")
	c.agentRuns = agentrun.New(path)
	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	sub := newMockAgent(providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("done"), nil
	})
	_, err = c.runSubAgent(t.Context(), subAgentParams{
		Agent:          sub,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "Map the parser entry points\nand then the tokenizer",
		SessionTitle:   "Subagent",
	})
	require.NoError(t, err)

	runs := agentrun.Load(path)
	require.Len(t, runs, 1)
	require.Equal(t, "Map the parser entry points", runs[0].Name)
}

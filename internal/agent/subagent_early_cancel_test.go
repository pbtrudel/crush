package agent

import (
	"context"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestCancelBeforeSubagentTrackingSticks pins the window between the run
// being listed and its agent being tracked: a click in the browser can
// arrive there, find no tracked agent, and be lost. SessionSetup runs
// before the run is listed, so calling Cancel from it stands in for a click
// arriving in that window; either way the cancel must be recorded before
// the run starts, and the run must start already cancelled.
func TestCancelBeforeSubagentTrackingSticks(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	c.dispatched = csync.NewMap[string, *dispatchedAgent]()
	c.agentRuns = agentrun.New(filepath.Join(t.TempDir(), "agents.json"))
	c.mainAgent = newMockAgent(providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("main answer"), nil
	})
	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	var sawCancel bool
	sub := newMockAgent(providerID, 4096, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		sawCancel = ctx.Err() != nil
		if sawCancel {
			return nil, ctx.Err()
		}
		return agentResultWithText("ran to completion"), nil
	})
	resp, err := c.runSubAgent(t.Context(), subAgentParams{
		Agent:          sub,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do something",
		SessionTitle:   "Subagent",
		SessionSetup:   func(id string) { c.Cancel(id) },
	})
	require.NoError(t, err)
	require.True(t, sawCancel, "a cancel while the run was listed but not tracked must reach the run")
	require.True(t, resp.IsError)
	childID := c.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	c.subSessionsMu.Lock()
	_, left := c.subCancels[childID]
	c.subSessionsMu.Unlock()
	require.False(t, left, "the finished run must drop its cancel")

	// The cancelled run must be listed as failed, not left running or
	// settled as if it had finished.
	runs := c.agentRuns.List()
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.StatusFailed, runs[0].Status)
	require.Equal(t, childID, runs[0].ID)
}

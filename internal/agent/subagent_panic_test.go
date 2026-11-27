package agent

import (
	"context"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSubAgentPanicReleasesTheBookkeeping pins that a panic in the run
// cannot leave the child session counted as busy: the release is deferred,
// so it happens on the way out however the run ends.
func TestSubAgentPanicReleasesTheBookkeeping(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	boom := func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		panic("the provider client blew up")
	}

	t.Run("synchronous run", func(t *testing.T) {
		t.Parallel()
		sub := newMockAgent(providerID, 4096, boom)
		childID := c.sessions.CreateAgentToolSessionID("msg-1", "call-1")
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = c.runSubAgent(t.Context(), subAgentParams{
				Agent:          sub,
				SessionID:      parent.ID,
				AgentMessageID: "msg-1",
				ToolCallID:     "call-1",
				Prompt:         "do something",
				SessionTitle:   "Subagent",
			})
		}()
		require.Equal(t, "the provider client blew up", recovered)
		require.False(t, c.subSessionBusy(childID), "the panicked run must leave the bookkeeping")
	})

	t.Run("dispatched run", func(t *testing.T) {
		t.Parallel()
		sub := newMockAgent(providerID, 4096, boom)
		sess, err := env.sessions.Create(t.Context(), "Dispatched")
		require.NoError(t, err)
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = c.executeSubAgent(t.Context(), sub, sess, subAgentParams{
				Agent:     sub,
				SessionID: parent.ID,
				Prompt:    "do something",
			})
		}()
		require.Equal(t, "the provider client blew up", recovered)
		require.False(t, c.subSessionBusy(sess.ID), "the panicked run must leave the bookkeeping")
	})
}

// TestSubAgentPanicSettlesTheRun pins that a panicking run is not left
// listed as running: the finish is deferred and records a failure.
func TestSubAgentPanicSettlesTheRun(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	c.agentRuns = agentrun.New(filepath.Join(t.TempDir(), "agents.json"))
	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	sub := newMockAgent(providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		panic("the provider client blew up")
	})
	func() {
		defer func() { _ = recover() }()
		_, _ = c.runSubAgent(t.Context(), subAgentParams{
			Agent: sub, SessionID: parent.ID, AgentMessageID: "msg-9", ToolCallID: "call-9",
			Prompt: "do something", SessionTitle: "Subagent",
		})
	}()
	childID := c.sessions.CreateAgentToolSessionID("msg-9", "call-9")
	var found bool
	for _, r := range c.agentRuns.List() {
		if r.ID == childID {
			found = true
			require.Equal(t, agentrun.StatusFailed, r.Status)
		}
	}
	require.True(t, found, "the run must stay listed, settled as failed")
}

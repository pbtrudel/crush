package agent

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// panickingPublisher panics on every publish, the way a buggy notifier
// would, so a run that reaches it must still settle in the registry.
type panickingPublisher struct{}

func (panickingPublisher) Publish(pubsub.EventType, notify.Notification) {
	panic("sign-in publish panicked")
}

func (panickingPublisher) PublishMustDeliver(context.Context, pubsub.EventType, notify.Notification) {
	panic("sign-in publish panicked")
}

// TestPanickingSignInHookSettlesTheRunFailed pins the run bookkeeping
// against a panic in the re-authentication notice: a clean run that panics
// while asking for a sign-in must be listed as failed, not as a run that
// finished.
func TestPanickingSignInHookSettlesTheRunFailed(t *testing.T) {
	const providerID = "copilot"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	coord.notify = panickingPublisher{}
	coord.agentRuns = agentrun.New(filepath.Join(t.TempDir(), "agents.json"))

	parent, err := env.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	agent := newMockAgent(providerID, 128, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, &fantasy.ProviderError{StatusCode: http.StatusUnauthorized}
	})
	agent.model.OAuth = true

	require.Panics(t, func() {
		_, _ = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parent.ID,
			AgentMessageID: "message",
			ToolCallID:     "call",
			Prompt:         "go",
			SessionTitle:   "sub",
		})
	})

	runs := coord.agentRuns.List()
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.StatusFailed, runs[0].Status,
		"a panic after a clean run is a failed run, not a finished one")
}

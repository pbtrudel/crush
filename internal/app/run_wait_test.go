package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/message"
)

// treeCoordinator answers its run at once and keeps its session tree busy
// until release is closed. The embedded interface is nil: the command calls
// nothing else on it.
type treeCoordinator struct {
	agent.Coordinator
	runErr  error
	release chan struct{}
}

func (c *treeCoordinator) Run(context.Context, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	if c.runErr != nil {
		return nil, c.runErr
	}
	return &fantasy.AgentResult{}, nil
}

func (c *treeCoordinator) WaitForSessionTree(ctx context.Context, _ string) (agent.SessionTree, error) {
	select {
	case <-c.release:
		return agent.SessionTree{Team: agentteam.TreeStatus{UnreadByLead: 1}}, nil
	case <-ctx.Done():
		return agent.SessionTree{Busy: true}, ctx.Err()
	}
}

func startRunAndWait(ctx context.Context, coordinator agent.Coordinator) <-chan runResponse {
	done := make(chan runResponse, 1)
	go func() { done <- runAndWait(ctx, coordinator, "session-root", "go", agent.RunLimits{}) }()
	return done
}

func TestRunAndWaitEndsWithTheSessionTree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		runErr error
	}{
		{name: "the first turn completed"},
		// A teammate started before the failure is still working.
		{name: "the first turn failed", runErr: errors.New("provider down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator := &treeCoordinator{runErr: tt.runErr, release: make(chan struct{})}
			done := startRunAndWait(t.Context(), coordinator)
			select {
			case <-done:
				t.Fatal("the command ended while its session tree was busy")
			case <-time.After(100 * time.Millisecond):
			}

			close(coordinator.release)
			select {
			case response := <-done:
				require.Equal(t, 1, response.tree.Team.UnreadByLead)
				if tt.runErr == nil {
					require.NoError(t, response.err)
				} else {
					require.ErrorIs(t, response.err, tt.runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the command did not end once its session tree was idle")
			}
		})
	}
}

// A signal cancels the command's context: the wait ends at once and the
// first turn's outcome is returned. Execute then exits 130 because its own
// signal context is done.
func TestRunAndWaitStopsWithItsContext(t *testing.T) {
	t.Parallel()
	coordinator := &treeCoordinator{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	done := startRunAndWait(ctx, coordinator)
	cancel()
	select {
	case response := <-done:
		require.NoError(t, response.err)
		require.True(t, response.tree.Busy)
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not stop with its context")
	}
}

// plainCoordinator cannot watch its tree, like the test doubles elsewhere.
type plainCoordinator struct{ agent.Coordinator }

func (plainCoordinator) Run(context.Context, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	return &fantasy.AgentResult{}, nil
}

func TestRunAndWaitWithoutATreeWatcher(t *testing.T) {
	t.Parallel()
	response := runAndWait(t.Context(), plainCoordinator{}, "session-root", "go", agent.RunLimits{})
	require.NoError(t, response.err)
	require.Equal(t, agent.SessionTree{}, response.tree)
}

// limitCoordinator records the limits the command set on the tree and how
// many prompts had run by then.
type limitCoordinator struct {
	plainCoordinator
	runs      int
	limits    []agent.RunLimits
	runsFirst int
}

func (c *limitCoordinator) Run(context.Context, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	c.runs++
	return &fantasy.AgentResult{}, nil
}

func (c *limitCoordinator) LimitSessionTree(_ string, limits agent.RunLimits) {
	c.limits = append(c.limits, limits)
	c.runsFirst = c.runs
}

// The limits are on the tree before the prompt runs, so the timeout counts
// from the prompt and the first step already draws on the budget.
func TestRunAndWaitLimitsTheTreeFirst(t *testing.T) {
	t.Parallel()
	limits := agent.RunLimits{MaxSteps: 5, Timeout: time.Minute}
	limited := &limitCoordinator{}
	runAndWait(t.Context(), limited, "session-root", "go", limits)
	require.Equal(t, []agent.RunLimits{limits}, limited.limits)
	require.Zero(t, limited.runsFirst, "the limits were set before the prompt ran")
	require.Equal(t, 1, limited.runs)

	unlimited := &limitCoordinator{}
	runAndWait(t.Context(), unlimited, "session-root", "go", agent.RunLimits{})
	require.Empty(t, unlimited.limits, "no limit, nothing to set")
}

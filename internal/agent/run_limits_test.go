package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

func TestTreeLimits(t *testing.T) {
	t.Parallel()

	var none *treeLimits
	none.takeStep()
	require.False(t, none.reached(), "a tree without limits is never stopped")

	unlimited := newTreeLimits(RunLimits{}, time.Now())
	unlimited.takeStep()
	require.False(t, unlimited.reached())

	budget := newTreeLimits(RunLimits{MaxSteps: 2}, time.Now())
	budget.takeStep()
	require.False(t, budget.stepsSpent())
	budget.takeStep()
	require.True(t, budget.stepsSpent())
	require.False(t, budget.deadlinePassed())
	require.True(t, budget.reached())

	future := newTreeLimits(RunLimits{Timeout: time.Hour}, time.Now())
	require.False(t, future.deadlinePassed())
	past := newTreeLimits(RunLimits{Timeout: time.Second}, time.Now().Add(-time.Minute))
	require.True(t, past.deadlinePassed())
	require.False(t, past.stepsSpent())
	require.True(t, past.reached())
}

// A run refused because its tree reached a limit writes nothing, so the
// refusal itself says which limit it was. The deadline is read first.
func TestTreeLimitsRefusedBy(t *testing.T) {
	t.Parallel()

	var none *treeLimits
	none.refuse()
	require.Empty(t, none.refusedBy(), "a tree without limits refuses nothing")

	unrefused := newTreeLimits(RunLimits{MaxSteps: 1}, time.Now())
	unrefused.takeStep()
	require.True(t, unrefused.stepsSpent())
	require.Empty(t, unrefused.refusedBy(), "a spent budget that refused no run reads empty")

	budget := newTreeLimits(RunLimits{MaxSteps: 1}, time.Now())
	budget.takeStep()
	budget.refuse()
	require.Equal(t, message.TurnEndStepLimit, budget.refusedBy())

	past := newTreeLimits(RunLimits{Timeout: time.Nanosecond}, time.Now().Add(-time.Minute))
	require.True(t, past.deadlinePassed())
	past.refuse()
	require.Equal(t, message.TurnEndTimeLimit, past.refusedBy(), "the deadline is read first")
}

// A run finds its tree's limits through its session's parent chain, so a
// teammate's subagent draws on the budget the command set on the Lead.
func TestRunLimitsFollowTheParentChain(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()
	root, err := env.sessions.Create(ctx, "Lead")
	require.NoError(t, err)
	teammate, err := env.sessions.CreateTaskSession(ctx, "team-1", root.ID, "Teammate")
	require.NoError(t, err)
	child, err := env.sessions.CreateTaskSession(ctx, "call-1", teammate.ID, "Subagent")
	require.NoError(t, err)
	other, err := env.sessions.Create(ctx, "Other")
	require.NoError(t, err)

	var registry runLimits
	require.Nil(t, registry.forSession(ctx, nil, child.ID), "nothing is read while no tree has limits")

	tree := registry.set(root.ID, RunLimits{MaxSteps: 3})
	require.Same(t, tree, registry.forSession(ctx, env.sessions, root.ID))
	require.Same(t, tree, registry.forSession(ctx, env.sessions, teammate.ID))
	require.Same(t, tree, registry.forSession(ctx, env.sessions, child.ID))
	require.Nil(t, registry.forSession(ctx, env.sessions, other.ID))
	require.Nil(t, registry.forSession(ctx, env.sessions, "no-such-session"))

	var none *runLimits
	require.Nil(t, none.forSession(ctx, env.sessions, child.ID))
}

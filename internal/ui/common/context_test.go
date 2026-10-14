package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceOpContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := WorkspaceOpContext()
	defer cancel()

	require.NoError(t, ctx.Err(), "the context must be live on creation")

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "workspace operations must be bounded by a deadline")
	remaining := time.Until(deadline)
	require.Greater(t, remaining, time.Duration(0), "the deadline must be in the future")
	require.LessOrEqual(t, remaining, WorkspaceOpTimeout, "the deadline must match the configured timeout")

	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

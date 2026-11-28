package webtest

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestSeedM1GivesTheShellEveryMarker pins the fixture the browser specs
// drive: four sessions, each in a different state.
func TestSeedM1GivesTheShellEveryMarker(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	SeedM1(f, clock)

	sessions, err := f.ListSessions(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	require.Contains(t, ids, "s-m1-run")
	require.Contains(t, ids, "s-m1-queue")
	require.Contains(t, ids, "s-m1-wait")
	require.Contains(t, ids, "s-m1-idle")

	require.True(t, f.AgentIsSessionBusy("s-m1-run"))
	require.Equal(t, []string{"and the golden files"}, f.AgentQueuedPromptsList("s-m1-queue"))
	require.False(t, f.AgentIsSessionBusy("s-m1-idle"))

	msgs, err := f.ListMessages(context.Background(), "s-m1-run")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
}

// TestSeedM1PendingOpensAPermission pins the fourth marker's source: a
// permission request the gateway's pending store keys to s-m1-wait.
func TestSeedM1PendingOpensAPermission(t *testing.T) {
	t.Parallel()
	f := NewFake()
	sub := f.SubscribeEvents(t.Context())
	SeedM1Pending(f)
	ev, ok := <-sub
	require.True(t, ok)
	req, isRequest := ev.(pubsub.Event[permission.PermissionRequest])
	require.True(t, isRequest, "the seed publishes a permission request, got %T", ev)
	require.Equal(t, "s-m1-wait", req.Payload.SessionID)
	require.Equal(t, "p-m1", req.Payload.ID)
}

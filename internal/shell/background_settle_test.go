package shell

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBackgroundFinisherIsDroppedAfterItRuns pins that a finished job does
// not keep its finisher, and the snapshot it owns, alive.
func TestBackgroundFinisherIsDroppedAfterItRuns(t *testing.T) {
	t.Parallel()
	manager := newBackgroundShellManager()
	job, err := manager.Start(t.Context(), nil, t.TempDir(), nil, "echo quick", "", testMaxJobs)
	require.NoError(t, err)
	job.SetFinisher(func() string { return "NOTE" })
	job.Wait()

	job.finishMu.Lock()
	finisher := job.finisher
	job.finishMu.Unlock()
	require.Nil(t, finisher, "the finisher is released once it has run")
	require.Equal(t, "NOTE", job.Note())
}

// TestKillSessionWaitsForTheFinishers pins the settle primitive: killing a
// session's jobs returns only after each one's finisher has run, and other
// sessions keep their jobs.
func TestKillSessionWaitsForTheFinishers(t *testing.T) {
	t.Parallel()
	manager := newBackgroundShellManager()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	mine, err := manager.StartWithSession(ctx, nil, t.TempDir(), nil, "sleep 30", "", "session-mine", testMaxJobs)
	require.NoError(t, err)
	checked := make(chan struct{})
	mine.SetFinisher(func() string {
		time.Sleep(50 * time.Millisecond)
		close(checked)
		return "checked"
	})
	theirs, err := manager.StartWithSession(ctx, nil, t.TempDir(), nil, "sleep 30", "", "session-theirs", testMaxJobs)
	require.NoError(t, err)
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer killCancel()
		_ = manager.Kill(killCtx, theirs.ID)
	})

	var running atomic.Bool
	running.Store(true)
	go func() {
		manager.KillSession(ctx, "session-mine")
		running.Store(false)
	}()
	select {
	case <-checked:
	case <-time.After(10 * time.Second):
		t.Fatal("the finisher never ran")
	}
	require.Eventually(t, func() bool { return !running.Load() }, 10*time.Second, 10*time.Millisecond)
	_, mineTracked := manager.Get(mine.ID)
	require.False(t, mineTracked, "a settled job leaves the manager")
	_, theirsTracked := manager.Get(theirs.ID)
	require.True(t, theirsTracked, "another session's job keeps running")
}

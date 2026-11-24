package web

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// TestShutdownHonoursACancelledContext pins that a watcher which cannot
// settle cannot keep Shutdown waiting past the caller's deadline. The
// gateway here has watchers that never report done and a server that has
// nothing to drain.
func TestShutdownHonoursACancelledContext(t *testing.T) {
	t.Parallel()
	drained := make(chan struct{})
	close(drained)
	g := &Gateway{
		hub:       newHub(),
		cancel:    func() {},
		teamDone:  make(chan struct{}),
		modelDone: make(chan struct{}),
		done:      drained,
		srv:       &http.Server{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	returned := make(chan error, 1)
	go func() { returned <- g.Shutdown(ctx) }()
	select {
	case err := <-returned:
		// A server with no connections and no listener has nothing to
		// drain, so the expired context changes only the wait on the team
		// watcher, not the answer.
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown waited on the team watcher despite a cancelled context")
	}
}

// blockedReadyFake blocks AgentReadyErr while blocked is set, so a test can
// hold a workspace read the way a client workspace held by an unreachable
// server does.
type blockedReadyFake struct {
	*webtest.Fake
	blocked atomic.Bool
	// entered receives once when a blocked read starts.
	entered chan struct{}
	release chan struct{}
}

func (f *blockedReadyFake) AgentReadyErr() error {
	if f.blocked.Load() {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-f.release
	}
	return f.Fake.AgentReadyErr()
}

// TestModelWatchStallDoesNotBlockEvents pins that a workspace read that
// never returns cannot stop event forwarding: a tab keeps receiving frames
// while the model check hangs.
func TestModelWatchStallDoesNotBlockEvents(t *testing.T) {
	t.Parallel()
	fake := &blockedReadyFake{
		Fake:    webtest.NewFake(),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	// Release first, since Shutdown waits for the model watch and this
	// cleanup runs before the one above.
	t.Cleanup(func() { close(fake.release) })

	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.

	fake.blocked.Store(true)
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the model watch never read the workspace")
	}

	fake.Emit(msgEvent("m1", "s1", "still flowing"))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, c, "message")
	require.Contains(t, string(f.Payload), "still flowing")
}

// TestShutdownBoundsTheModelWatch pins that a stalled model watch cannot
// hold Shutdown past the caller's deadline.
func TestShutdownBoundsTheModelWatch(t *testing.T) {
	t.Parallel()
	fake := &blockedReadyFake{
		Fake:    webtest.NewFake(),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { close(fake.release) })

	fake.blocked.Store(true)
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the model watch never read the workspace")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(shutdownCtx) }()
	select {
	case err := <-done:
		require.NoError(t, err)
		require.Less(t, time.Since(start), time.Second, "Shutdown waited on a stalled model watch")
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown waited on a stalled model watch")
	}
}

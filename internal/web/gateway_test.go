package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestShutdownHonoursACancelledContext pins that a watcher which cannot
// settle cannot keep Shutdown waiting past the caller's deadline. The
// gateway here has a team watcher that never reports done and a server that
// has nothing to drain.
func TestShutdownHonoursACancelledContext(t *testing.T) {
	t.Parallel()
	drained := make(chan struct{})
	close(drained)
	g := &Gateway{
		hub:      newHub(),
		cancel:   func() {},
		teamDone: make(chan struct{}),
		done:     drained,
		srv:      &http.Server{},
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

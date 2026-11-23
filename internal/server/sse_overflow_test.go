package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// stalledWriter is a ResponseWriter whose writes block until resume is
// closed, like a client that stopped reading its stream.
type stalledWriter struct {
	header http.Header
	resume <-chan struct{}
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Write(p []byte) (int, error) {
	<-w.resume
	return len(p), nil
}

// TestE2E_StalledStreamClosedOnOverflow guards against keeping a client's
// event stream open with a silent gap after events were dropped for it.
func TestE2E_StalledStreamClosedOnOverflow(t *testing.T) {
	t.Parallel()
	h := newE2EHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	resume := make(chan struct{})
	served := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-resume:
		default:
			close(resume)
		}
		<-served
	})

	cid := uuid.New().String()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/v1/workspaces/"+h.workspace.ID+"/events?client_id="+cid, nil)
	req.Host = "localhost"
	go func() {
		defer close(served)
		h.srv.Handler().ServeHTTP(&stalledWriter{header: http.Header{}, resume: resume}, req)
	}()
	h.waitForAttached(t, 1)
	h.setCurrentSession(t, ctx, h.workspace.ID, cid, "s-overflow")

	// Overflow pubsub's 4096-event buffer for the stalled subscriber. Each
	// event updates a different message, so the broker cannot coalesce them.
	for i := range 4096 + 64 {
		h.app.SendEvent(pubsub.Event[message.Message]{
			Type:    pubsub.UpdatedEvent,
			Payload: message.Message{ID: fmt.Sprintf("m-%d", i), SessionID: "s-overflow", Role: message.Assistant},
		})
	}
	// Events reach a client through the workspace's shared sink, whose pump
	// can still be working through the broker's queue when the loop above
	// returns. The client is told to resync either when the pump pushes more
	// frames than its backlog holds or, under load, when the pump itself
	// falls behind the broker, so wait for either before letting the writer
	// go.
	require.Eventually(t, func() bool { return sinkSignalledOverflow(h.srv, h.workspace.ID) },
		10*time.Second, 10*time.Millisecond, "the stalled client never fell behind")
	close(resume)
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream of a client that fell behind was left open")
	}
}

// sinkSignalledOverflow reports whether a client attached to the shared
// event sink of workspaceID has been told it lost events.
func sinkSignalledOverflow(srv *Server, workspaceID string) bool {
	srv.sinks.mu.Lock()
	reg, ok := srv.sinks.byName[workspaceID]
	srv.sinks.mu.Unlock()
	if !ok {
		return false
	}
	reg.sink.mu.Lock()
	defer reg.sink.mu.Unlock()
	for sub := range reg.sink.subs {
		select {
		case <-sub.overflowed():
			return true
		default:
		}
	}
	return false
}

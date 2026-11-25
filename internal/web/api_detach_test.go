package web

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetachClosesEveryTab(t *testing.T) {
	t.Parallel()
	detached := make(chan struct{})
	g, _ := startWith(t, Options{OnDetach: func() { close(detached) }})
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/detach", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	select {
	case <-detached:
	case <-time.After(time.Second):
		t.Fatal("the controller was not asked to stop web mode")
	}
	require.NoError(t, g.Shutdown(context.Background()))
	fr := nextFrameOfType(t.Context(), t, c, frameClosing)
	require.JSONEq(t, `{"reason":"detached"}`, string(fr.Payload))
}

func TestDetachWithoutTerminal503(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/detach", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode)
	require.Contains(t, string(body), "no_terminal")
}

// TestDetachRunsTheCallbackOnce checks that a double click stops web mode
// once: the second request answers 202 but asks the terminal nothing.
func TestDetachRunsTheCallbackOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	g, _ := startWith(t, Options{OnDetach: func() {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		<-release
	}})
	for range 2 {
		rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/detach", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("the detach callback did not run")
	}
	require.Equal(t, int32(1), calls.Load(), "a second detach must not call the callback again")
	close(release)
}

// TestDetachAnswersBeforeShutdown checks that the 202 is flushed before the
// callback the route starts runs: the request completes while the callback
// is still blocked, so a shutdown it performs cannot swallow the answer.
func TestDetachAnswersBeforeShutdown(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	g, _ := startWith(t, Options{OnDetach: func() {
		close(entered)
		<-release
	}})
	// Registered after startWith's cleanup, so it runs before the gateway
	// shutdown that would otherwise wait for the blocked handler.
	t.Cleanup(unblock)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL()+"/api/detach", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", g.URL())
	rsp := doReq(t, authedClient(t, g), req)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, "the route answers before the shutdown it starts")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("the detach callback did not run")
	}
	// The answer arrived while the callback was still blocked here.
	unblock()
}

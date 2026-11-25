package web

import (
	"context"
	"net/http"
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

package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// startPlanTest starts a gateway whose plan confirms go to fn, which may be
// nil to model a gateway with no terminal attached.
func startPlanTest(t *testing.T, fn func(string, bool)) *Gateway {
	t.Helper()
	g, err := Start(t.Context(), Options{Workspace: webtest.NewFake(), OnPlanHandoff: fn})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g
}

func TestPlanHandoffAsksTheTerminal(t *testing.T) {
	t.Parallel()

	type call struct {
		sessionID string
		yolo      bool
	}

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()
		calls := make(chan call, 1)
		g := startPlanTest(t, func(id string, yolo bool) { calls <- call{sessionID: id, yolo: yolo} })
		g.SetPlanReady("s1")

		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/plan/handoff",
			`{"session_id":"s1","action":"code_yolo"}`)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))

		select {
		case got := <-calls:
			require.Equal(t, call{sessionID: "s1", yolo: true}, got)
		case <-time.After(2 * time.Second):
			t.Fatal("the terminal was not asked to confirm the plan")
		}
	})

	t.Run("bad action", func(t *testing.T) {
		t.Parallel()
		calls := make(chan call, 1)
		g := startPlanTest(t, func(id string, yolo bool) { calls <- call{sessionID: id, yolo: yolo} })
		g.SetPlanReady("s1")

		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/plan/handoff",
			`{"session_id":"s1","action":"plan"}`)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"bad_action"`)
		require.Empty(t, calls, "a bad action must not reach the terminal")
	})

	t.Run("wrong session", func(t *testing.T) {
		t.Parallel()
		calls := make(chan call, 1)
		g := startPlanTest(t, func(id string, yolo bool) { calls <- call{sessionID: id, yolo: yolo} })
		g.SetPlanReady("s1")

		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/plan/handoff",
			`{"session_id":"s2","action":"code"}`)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"plan_not_ready"`)
		require.Empty(t, calls, "another session's plan must not reach the terminal")
	})

	t.Run("empty session", func(t *testing.T) {
		t.Parallel()
		calls := make(chan call, 1)
		g := startPlanTest(t, func(id string, yolo bool) { calls <- call{sessionID: id, yolo: yolo} })
		g.SetPlanReady("s1")

		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/plan/handoff",
			`{"session_id":"","action":"code"}`)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"plan_not_ready"`)
		require.Empty(t, calls)
	})

	t.Run("no terminal", func(t *testing.T) {
		t.Parallel()
		g := startPlanTest(t, nil)
		g.SetPlanReady("s1")

		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/plan/handoff",
			`{"session_id":"s1","action":"code"}`)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"agent_starting"`)
	})
}

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// The terminal reports a pending ready plan through an optional launcher
// interface, so the real controller must keep matching it.
var _ interface {
	SetPlanReady(sessionID string, seq uint64)
} = (*Controller)(nil)

func TestControllerOpenStartsOnceAndOpensBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	require.False(t, c.Running())
	u1, err := c.Open(t.Context())
	require.NoError(t, err)
	require.True(t, c.Running())
	u2, err := c.Open(t.Context())
	require.NoError(t, err)
	require.Equal(t, u1, u2)
	require.Equal(t, []string{u1, u1}, opened)
	require.True(t, strings.HasPrefix(u1, "http://127.0.0.1:"))
}

func TestControllerOpenBrowserFailureKeepsURL(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	c.open = func(string) error { return errors.New("no display") }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	u, err := c.Open(t.Context())
	require.Error(t, err)
	require.Contains(t, u, "/#t=")
	require.True(t, c.Running())
}

func TestControllerShutdownIdempotent(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	require.NoError(t, c.Shutdown(t.Context()))
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	require.NoError(t, c.Shutdown(t.Context()))
	require.False(t, c.Running())
	require.NoError(t, c.Shutdown(t.Context()))
}

func TestControllerStartDoesNotOpenBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	u, err := c.Start(t.Context())
	require.NoError(t, err)
	require.Empty(t, opened, "Start must not open the browser")
	require.Equal(t, u, c.LaunchURL())
	require.NoError(t, c.OpenBrowser())
	require.Equal(t, []string{u}, opened)
}

func TestControllerOpenBrowserRequiresGateway(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	called := false
	c.open = func(string) error { called = true; return nil }

	require.Error(t, c.OpenBrowser())
	require.False(t, called)
}

// TestControllerForwardsSessionsCreatedInBrowser checks that the callback
// set after Start reaches the running gateway, where the root command sets
// it once the program exists.
func TestControllerForwardsSessionsCreatedInBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	created := make(chan string, 1)
	c.SetOnSessionCreated(func(id string) { created <- id })

	rsp, data := doJSON(t, authedClient(t, c.gw), http.MethodPost, c.gw.URL()+"/api/sessions", "{}")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusCreated, rsp.StatusCode)

	var sess struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(data, &sess))
	select {
	case id := <-created:
		require.Equal(t, sess.ID, id)
	case <-time.After(2 * time.Second):
		t.Fatal("the controller did not forward the created session")
	}
}

// TestControllerForwardsInputModeRequests checks that the callback set
// after Start reaches the running gateway.
func TestControllerForwardsInputModeRequests(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	plan := make(chan bool, 1)
	c.SetOnInputModeRequested(func(v bool) { plan <- v })

	rsp, _ := doJSON(t, authedClient(t, c.gw), http.MethodPost, c.gw.URL()+"/api/input-mode", `{"mode":"plan"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	select {
	case v := <-plan:
		require.True(t, v)
	case <-time.After(2 * time.Second):
		t.Fatal("the controller did not forward the input mode request")
	}
}

// TestControllerReportsInputModeBeforeStart checks that a mode reported
// before the gateway exists is in the snapshot the first tab receives.
func TestControllerReportsInputModeBeforeStart(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	c.SetInputMode(true, 1)
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	rsp, data := doJSON(t, authedClient(t, c.gw), http.MethodGet, c.gw.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var snap struct {
		InputMode string `json:"input_mode"`
	}
	require.NoError(t, json.Unmarshal(data, &snap))
	require.Equal(t, "plan", snap.InputMode)
}

// TestControllerIgnoresStaleInputModeReports checks that reports racing in
// separate commands cannot leave the gateway on the older mode.
func TestControllerIgnoresStaleInputModeReports(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	c.SetInputMode(true, 2)
	c.SetInputMode(false, 1)

	require.Equal(t, inputModePlan, c.gw.InputMode())
}

// TestControllerReportsInputModeRefusal checks that the terminal's busy
// refusal reaches the browser through the stream.
func TestControllerReportsInputModeRefusal(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	conn := dialStream(t, c.gw, "")
	readFrame(t, conn)
	c.ReportInputModeRefused("Can't switch while Crush is busy")
	f := readFrame(t, conn)
	require.Equal(t, frameError, f.Type)
	require.JSONEq(t, `{"error":"Can't switch while Crush is busy"}`, string(f.Payload))
}

func TestControllerConcurrentStartsShareOneGateway(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	urls := make(chan string, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := c.Start(t.Context())
			if err == nil {
				urls <- u
			}
		}()
	}
	wg.Wait()
	close(urls)
	first := <-urls
	for u := range urls {
		require.Equal(t, first, u)
	}
}

func TestControllerRestartsAfterShutdown(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	first, err := c.Start(t.Context())
	require.NoError(t, err)
	require.NoError(t, c.Shutdown(context.Background()))
	second, err := c.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	require.NotEqual(t, first, second, "a restart gets a new port and token")
}

// TestPlanReadyPublishesAndSnapshots checks the plan_ready frame and the
// snapshot field: a set publishes once, a repeat publishes nothing and an
// empty id clears both.
func TestPlanReadyPublishesAndSnapshots(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c)

	g.SetPlanReady("s1")
	f := readFrame(t, c)
	require.Equal(t, framePlanReady, f.Type)
	require.JSONEq(t, `{"session_id":"s1"}`, string(f.Payload))
	require.Equal(t, "s1", g.PlanReadySessionID())
	require.Equal(t, "s1", g.buildSnapshot(t.Context(), "s1").PlanReadySessionID)

	// A repeat changes nothing, so the next frame the tab sees is the
	// clear rather than a second report of the same session.
	g.SetPlanReady("s1")
	g.SetPlanReady("")
	f = readFrame(t, c)
	require.Equal(t, framePlanReady, f.Type)
	require.JSONEq(t, `{"session_id":""}`, string(f.Payload))
	require.Empty(t, g.PlanReadySessionID())
	require.Empty(t, g.buildSnapshot(t.Context(), "s1").PlanReadySessionID)
}

// TestControllerReportsPlanReadyBeforeStart checks that a pending plan
// reported before the gateway exists is in the snapshot the first tab
// receives.
func TestControllerReportsPlanReadyBeforeStart(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	c.SetPlanReady("s1", 1)
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	rsp, data := doJSON(t, authedClient(t, c.gw), http.MethodGet, c.gw.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var snap struct {
		PlanReadySessionID string `json:"plan_ready_session_id"`
	}
	require.NoError(t, json.Unmarshal(data, &snap))
	require.Equal(t, "s1", snap.PlanReadySessionID)
}

// TestControllerIgnoresStalePlanReadyReports checks that reports racing in
// separate commands cannot leave the gateway on the older plan.
func TestControllerIgnoresStalePlanReadyReports(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	c.SetPlanReady("s2", 2)
	c.SetPlanReady("s1", 1)

	require.Equal(t, "s2", c.gw.PlanReadySessionID())
}

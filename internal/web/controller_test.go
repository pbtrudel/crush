package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
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
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	c.isWSL = func() bool { return false }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	require.False(t, c.Running())
	u1, err := c.Open(t.Context())
	require.NoError(t, err)
	require.True(t, c.Running())
	u2, err := c.Open(t.Context())
	require.NoError(t, err)
	require.Equal(t, u1, u2)
	require.Len(t, opened, 2)
	require.Equal(t, opened[0], opened[1], "both opens use the same launch file")
	require.True(t, strings.HasPrefix(opened[0], "file://"), opened[0])
	require.NotContains(t, opened[0], strings.TrimPrefix(u1, c.gw.URL()+"/#t="))
	require.True(t, strings.HasPrefix(u1, "http://127.0.0.1:"))
}

func TestControllerOpenBrowserFailureKeepsURL(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	c.open = func(string) error { return errors.New("no display") }
	c.isWSL = func() bool { return false }
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
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	c.isWSL = func() bool { return false }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	u, err := c.Start(t.Context())
	require.NoError(t, err)
	require.Empty(t, opened, "Start must not open the browser")
	require.Equal(t, u, c.LaunchURL())
	require.NoError(t, c.OpenBrowser())
	require.Len(t, opened, 1)
	require.True(t, strings.HasPrefix(opened[0], "file://"), opened[0])
	require.NotContains(t, opened[0], strings.TrimPrefix(u, c.gw.URL()+"/#t="))
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
	// The browser reads the code to pick the toast tone; a frame without
	// one falls through to a generic error.
	require.JSONEq(t, `{"session_id":"","code":"agent_busy","error":"Can't switch while Crush is busy"}`, string(f.Payload))
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

func TestOpenBrowserPassesNoToken(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var got string
	c := NewController(webtest.NewFake())
	c.open = func(u string) error { got = u; return nil }
	c.isWSL = func() bool { return false }
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	require.NoError(t, c.OpenBrowser())
	require.True(t, strings.HasPrefix(got, "file://"), got)
	require.NotContains(t, got, strings.TrimPrefix(c.LaunchURL(), c.gw.URL()+"/#t="))
}

func TestOpenBrowserOnWSLUsesTheUNCPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var got string
	c := NewController(webtest.NewFake())
	c.open = func(u string) error { got = u; return nil }
	c.isWSL = func() bool { return true }
	c.wslPath = func(_ context.Context, p string) (string, error) {
		return `\\wsl.localhost\Ubuntu` + strings.ReplaceAll(p, "/", `\`), nil
	}
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	require.NoError(t, c.OpenBrowser())
	require.True(t, strings.HasPrefix(got, "file://wsl.localhost/Ubuntu/"), got)
	require.True(t, strings.HasSuffix(got, "/launch.html"), got)
}

func TestOpenBrowserOnWSLWithoutWslpathNeverOpensTheToken(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	called := false
	c := NewController(webtest.NewFake())
	c.open = func(string) error { called = true; return nil }
	c.isWSL = func() bool { return true }
	c.wslPath = func(context.Context, string) (string, error) { return "", errors.New("wslpath: not found") }
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	require.Error(t, c.OpenBrowser())
	require.False(t, called, "the opener never receives the token URL")
}

func TestWindowsFileURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`\\wsl.localhost\Ubuntu\run\user\1000\crush-web-1\launch.html`: "file://wsl.localhost/Ubuntu/run/user/1000/crush-web-1/launch.html",
		`C:\Users\u\AppData\Local\Temp\crush-web-1\launch.html`:        "file:///C:/Users/u/AppData/Local/Temp/crush-web-1/launch.html",
	} {
		got, err := windowsFileURL(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"", "/tmp/x", `relative\x`} {
		_, err := windowsFileURL(bad)
		require.Error(t, err, bad)
	}
}

func TestLaunchFileExpiresUnused(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	g, _ := startWith(t, Options{launchFileTTL: 50 * time.Millisecond})
	path, err := g.LaunchFile()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return errors.Is(err, fs.ErrNotExist)
	}, 2*time.Second, 10*time.Millisecond, "a launch file no tab used does not linger")
}

// TestControllerDetachStopsAndReports checks that a browser detach stops
// the gateway, tells the terminal why, and leaves /web free to start a new
// gateway with a new token.
func TestControllerDetachStopsAndReports(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	stopped := make(chan string, 1)
	c.SetOnStopped(func(reason string) { stopped <- reason })

	u1, err := c.Start(t.Context())
	require.NoError(t, err)
	gw := c.gw
	rsp, _ := doJSON(t, authedClient(t, gw), http.MethodPost, gw.URL()+"/api/detach", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	require.Eventually(t, func() bool { return !c.Running() }, time.Second, 10*time.Millisecond,
		"the detach stops the gateway")
	select {
	case reason := <-stopped:
		require.Equal(t, "detached", reason)
	case <-time.After(time.Second):
		t.Fatal("the terminal was not told why web mode stopped")
	}

	u2, err := c.Start(t.Context())
	require.NoError(t, err)
	tokenOf := func(u string) string { _, token, _ := strings.Cut(u, "/#t="); return token }
	require.NotEmpty(t, tokenOf(u1))
	require.NotEqual(t, tokenOf(u1), tokenOf(u2), "a new gateway gets a new token")
}

// TestControllerDetachReportsOnce checks that a double click reports the
// stop once. The controller lock is held across both requests so both
// detach callbacks queue before either can stop the gateway, which is the
// window a second click lands in.
func TestControllerDetachReportsOnce(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	stopped := make(chan string, 2)
	c.SetOnStopped(func(reason string) { stopped <- reason })
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	gw := c.gw

	c.mu.Lock()
	for range 2 {
		rsp, _ := doJSON(t, authedClient(t, gw), http.MethodPost, gw.URL()+"/api/detach", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	}
	c.mu.Unlock()
	select {
	case reason := <-stopped:
		require.Equal(t, "detached", reason)
	case <-time.After(time.Second):
		t.Fatal("the terminal was not told web mode stopped")
	}
	require.False(t, c.Running())
	select {
	case reason := <-stopped:
		t.Fatalf("the second detach reported again: %q", reason)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestControllerDetachFromAnOldGatewayLeavesTheNewOneRunning checks that a
// detach callback that arrives after /web started a new gateway stops
// nothing: it belongs to the gateway that is already gone.
func TestControllerDetachFromAnOldGatewayLeavesTheNewOneRunning(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	stopped := make(chan string, 1)
	c.SetOnStopped(func(reason string) { stopped <- reason })
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	old := c.gw
	// The terminal stops web mode, /web starts a new gateway, and the old
	// gateway's delayed detach callback only now runs.
	require.NoError(t, c.Shutdown(context.Background()))
	_, err = c.Start(t.Context())
	require.NoError(t, err)
	current := c.gw
	require.NotSame(t, old, current)

	c.detach(old)
	require.True(t, c.Running(), "a stale detach must not stop the new gateway")
	require.Same(t, current, c.gw)
	rsp, body := doJSON(t, authedClient(t, current), http.MethodGet, current.URL()+"/api/health", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	select {
	case reason := <-stopped:
		t.Fatalf("a stale detach reported a stop: %q", reason)
	default:
	}
}

// TestControllerStopStopsTheGateway checks that a terminal stop shuts the
// gateway the way a browser detach does: the controller stops running, the
// old link no longer answers, and a new start brings up a fresh link.
func TestControllerStopStopsTheGateway(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	u1, err := c.Start(t.Context())
	require.NoError(t, err)
	old := c.gw
	require.True(t, c.Running())

	require.NoError(t, c.Stop(t.Context()))
	require.False(t, c.Running(), "the stop clears the gateway")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, old.URL()+"/api/health", nil)
	require.NoError(t, err)
	rsp, err := http.DefaultClient.Do(req)
	if rsp != nil {
		rsp.Body.Close()
	}
	require.Error(t, err, "the stopped gateway no longer answers")

	u2, err := c.Start(t.Context())
	require.NoError(t, err)
	require.True(t, c.Running())
	tokenOf := func(u string) string { _, token, _ := strings.Cut(u, "/#t="); return token }
	require.NotEmpty(t, tokenOf(u1))
	require.NotEqual(t, tokenOf(u1), tokenOf(u2), "a new gateway gets a new token")

	rsp, body := doJSON(t, authedClient(t, c.gw), http.MethodGet, c.gw.URL()+"/api/health", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
}

// TestControllerStopRequiresGateway checks that stopping web mode when it
// is not running reports an error instead of succeeding silently.
func TestControllerStopRequiresGateway(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	require.Error(t, c.Stop(t.Context()))

	require.NoError(t, c.Shutdown(t.Context()))
	require.Error(t, c.Stop(t.Context()), "a stopped controller has nothing to stop")
}

// TestControllerStopClosesTabsWithTheDetachNotice checks that every tab
// learns the gateway is gone on a terminal stop and shows the same notice
// a browser detach shows.
func TestControllerStopClosesTabsWithTheDetachNotice(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	gw := c.gw
	conn := dialStream(t, gw, "")
	readFrame(t, conn) // Snapshot.

	require.NoError(t, c.Stop(t.Context()))
	fr := nextFrameOfType(t.Context(), t, conn, frameClosing)
	require.JSONEq(t, `{"reason":"detached"}`, string(fr.Payload))
}

// TestControllerStopDoesNotReportThroughOnStopped checks that a terminal
// stop leaves the report to the caller: the onStopped callback speaks for a
// browser detach and would show the wrong message.
func TestControllerStopDoesNotReportThroughOnStopped(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	stopped := make(chan string, 1)
	c.SetOnStopped(func(reason string) { stopped <- reason })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	require.NoError(t, c.Stop(t.Context()))
	select {
	case reason := <-stopped:
		t.Fatalf("a terminal stop reported through onStopped: %q", reason)
	case <-time.After(100 * time.Millisecond):
	}
}

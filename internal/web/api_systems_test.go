package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// startAuthTest starts a gateway whose auth requests wait only a moment
// for an OAuth URL, so a request that finds none does not hold the test
// for the production three seconds.
func startAuthTest(t *testing.T, fake *webtest.Fake) *Gateway {
	t.Helper()
	g, err := Start(t.Context(), Options{Workspace: fake, mcpAuthPoll: 20 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g
}

// authFlows reports how many OAuth flows the gateway still holds, so a test
// can wait for a finished flow's entry to be removed.
func authFlows(g *Gateway) int {
	g.authMu.Lock()
	defer g.authMu.Unlock()
	return len(g.auths)
}

// countCalls counts exact call log entries.
func countCalls(fake *webtest.Fake, call string) int {
	n := 0
	for _, c := range fake.CallLog() {
		if c == call {
			n++
		}
	}
	return n
}

// readErrorFrame reads frames until the first web_error, skipping the
// snapshot and the live frames the gateway may send in between.
func readErrorFrame(t *testing.T, c *websocket.Conn) testFrame {
	t.Helper()
	for range 20 {
		f := readFrame(t, c)
		if f.Type == frameError {
			return f
		}
	}
	t.Fatal("no web_error frame arrived")
	return testFrame{}
}

// TestMCPListMergesDisabledAndPendingAuth pins the list's merge rules: a
// repository override counts as disabled unless an enabled override wins,
// a StateDisabled server is disabled whatever the overrides say, and a
// pending server that is disabled for this repository is not flagged for
// authentication (the TUI's filterAuthPending rule).
func TestMCPListMergesDisabledAndPendingAuth(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{
		"alpha": {Name: "alpha", State: mcp.StateNeedsAuth, Counts: mcp.Counts{Tools: 2, Prompts: 1}},
		"beta":  {Name: "beta", State: mcp.StateConnected},
		"delta": {Name: "delta", State: mcp.StateNeedsAuth},
		"echo":  {Name: "echo", State: mcp.StateError, Error: errors.New("boom")},
		"gamma": {Name: "gamma", State: mcp.StateDisabled},
	}
	fake.MCPDisabled = []string{"beta", "gamma", "delta"}
	fake.MCPEnabled = []string{"gamma"}
	fake.MCPPending = []mcp.PendingAuthServer{{Name: "alpha"}, {Name: "delta"}}

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/mcp", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	var out struct {
		Servers []mcpServerInfo `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, []mcpServerInfo{
		{Name: "alpha", State: "needs auth", Tools: 2, Prompts: 1, NeedsAuth: true},
		{Name: "beta", State: "connected", Disabled: true},
		{Name: "delta", State: "needs auth", Disabled: true},
		{Name: "echo", State: "error", Error: "boom"},
		{Name: "gamma", State: "disabled", Disabled: true},
	}, out.Servers)
}

// TestMCPListFailsOpenWhenOverridesCannotBeRead covers a session store
// failure: the list still answers, no server is marked disabled for this
// repository and a pending server still asks for authentication, which is
// what the TUI's filterAuthPending does.
func TestMCPListFailsOpenWhenOverridesCannotBeRead(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{
		"alpha": {Name: "alpha", State: mcp.StateNeedsAuth},
		"beta":  {Name: "beta", State: mcp.StateConnected},
	}
	fake.MCPDisabled = []string{"beta"}
	fake.MCPPending = []mcp.PendingAuthServer{{Name: "alpha"}}
	fake.SetMCPOverridesErr(errors.New("session store closed"))

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/mcp", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var out struct {
		Servers []mcpServerInfo `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, []mcpServerInfo{
		{Name: "alpha", State: "needs auth", NeedsAuth: true},
		{Name: "beta", State: "connected"},
	}, out.Servers)
}

// TestMCPActionOnUnreadableDisableList covers a session store failure
// during the known-server check: a live server still takes its action, and
// a name that is not live answers the unavailable state instead of
// claiming no such server exists, which the list could not have shown
// either.
func TestMCPActionOnUnreadableDisableList(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{"live": {Name: "live", State: mcp.StateConnected}}
	fake.MCPDisabled = []string{"off"}
	fake.SetMCPOverridesErr(errors.New("session store closed"))
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/live/start", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "MCPStartServer:live")
	}, time.Second, 10*time.Millisecond, "a live server is known whatever the overrides say")

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/off/enable", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "mcp_state_unavailable")
	require.NotContains(t, string(body), "unknown_server")
	require.NotContains(t, fake.CallLog(), "MCPSetServerDisabled:off:false",
		"a name the gateway cannot check reaches no workspace call")
}

func TestMCPActionsCallTheWorkspace(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{"srv": {Name: "srv", State: mcp.StateConnected}}
	c := authedClient(t, g)

	for _, tc := range []struct{ action, call string }{
		{"start", "MCPStartServer:srv"},
		{"disable", "MCPSetServerDisabled:srv:true"},
		{"enable", "MCPSetServerDisabled:srv:false"},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/srv/"+tc.action, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, tc.action, string(body))
		require.Eventually(t, func() bool {
			return slices.Contains(fake.CallLog(), tc.call)
		}, time.Second, 10*time.Millisecond, tc.call)
	}

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/srv/refresh", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		calls := fake.CallLog()
		return slices.Contains(calls, "RefreshMCPTools:srv") &&
			slices.Contains(calls, "MCPRefreshPrompts:srv") &&
			slices.Contains(calls, "MCPRefreshResources:srv")
	}, time.Second, 10*time.Millisecond)
}

func TestMCPUnknownAction404(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{"srv": {Name: "srv", State: mcp.StateConnected}}
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/pause", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_action")
	require.Empty(t, fake.CallLog())
}

func TestMCPUnknownServer404(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	for _, action := range []string{"start", "refresh", "enable", "disable", "auth", "enable_global", "disable_global", "auth_cancel"} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/ghost/"+action, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, action, string(body))
		require.Contains(t, string(body), "unknown_server", action)
	}
	require.Empty(t, fake.CallLog())

	// A server the repository disabled is known even without a live state,
	// so it can be turned back on.
	t.Run("disabled server is known", func(t *testing.T) {
		t.Parallel()
		fake := webtest.NewFake()
		fake.MCPDisabled = []string{"off"}
		g := startFake(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/off/enable", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		require.Eventually(t, func() bool {
			return slices.Contains(fake.CallLog(), "MCPSetServerDisabled:off:false")
		}, time.Second, 10*time.Millisecond)
	})
}

// TestMCPPromptsStartReachesTheActionHandler pins M31: the literal
// /api/mcp/prompts/run route shadows a server named "prompts" for one
// action only, and every other action on that name reaches the generic
// handler.
func TestMCPPromptsStartReachesTheActionHandler(t *testing.T) {
	t.Parallel()
	t.Run("unknown server", func(t *testing.T) {
		t.Parallel()
		g, _ := startTest(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/prompts/start", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
		require.Contains(t, string(body), "unknown_server")
	})
	t.Run("server named prompts", func(t *testing.T) {
		t.Parallel()
		g, fake := startTest(t)
		fake.MCP = map[string]mcp.ClientInfo{"prompts": {Name: "prompts", State: mcp.StateConnected}}
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/prompts/start", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		require.Eventually(t, func() bool {
			return slices.Contains(fake.CallLog(), "MCPStartServer:prompts")
		}, time.Second, 10*time.Millisecond)
	})
}

// newAuthFake returns a fake with one server awaiting OAuth.
func newAuthFake() *webtest.Fake {
	fake := webtest.NewFake()
	fake.MCP = map[string]mcp.ClientInfo{"srv": {Name: "srv", State: mcp.StateNeedsAuth}}
	fake.MCPPending = []mcp.PendingAuthServer{{Name: "srv"}}
	return fake
}

func TestMCPAuthReturnsOnlyWebURLs(t *testing.T) {
	t.Parallel()
	t.Run("https", func(t *testing.T) {
		t.Parallel()
		fake := newAuthFake()
		fake.AuthURL = "https://auth.example/x"
		g := startAuthTest(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
		defer rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.JSONEq(t, `{"url":"https://auth.example/x"}`, string(body))
		require.Eventually(t, func() bool {
			return slices.Contains(fake.CallLog(), "MCPAuthenticate:srv")
		}, time.Second, 10*time.Millisecond)
	})
	t.Run("javascript", func(t *testing.T) {
		t.Parallel()
		fake := newAuthFake()
		fake.AuthURL = "javascript:alert(1)"
		g := startAuthTest(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
		defer rsp.Body.Close()
		require.Equal(t, http.StatusBadGateway, rsp.StatusCode, string(body))
		require.Contains(t, string(body), "bad_auth_url")
	})
	t.Run("no url", func(t *testing.T) {
		t.Parallel()
		fake := newAuthFake()
		g := startAuthTest(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
		defer rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.JSONEq(t, `{"url":""}`, string(body))
	})
	t.Run("not pending", func(t *testing.T) {
		t.Parallel()
		fake := webtest.NewFake()
		fake.MCP = map[string]mcp.ClientInfo{"other": {Name: "other", State: mcp.StateConnected}}
		g := startAuthTest(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/other/auth", "")
		defer rsp.Body.Close()
		require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
		require.Contains(t, string(body), "auth_not_needed")
		require.Empty(t, fake.CallLog(), "a server that is not pending never starts a flow")
	})
}

// TestMCPAuthRefusedForRepoDisabledServer pins that a server disabled for
// this repository never starts an OAuth flow, the same filter the list
// applies: disabling a server that needs a sign-in must stop the prompt.
func TestMCPAuthRefusedForRepoDisabledServer(t *testing.T) {
	t.Parallel()
	fake := newAuthFake()
	fake.MCPDisabled = []string{"srv"}
	g := startAuthTest(t, fake)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "auth_not_needed")
	require.NotContains(t, fake.CallLog(), "MCPAuthenticate:srv",
		"a server disabled for this repository starts no flow")
}

// TestMCPAuthReusesTheRunningFlow pins that a second request for a name
// whose flow is still running starts nothing, answers the current URL, and
// that the flow outlives the request that started it. The flow moves the
// server out of the pending state as soon as it starts, so the reusing
// request cannot depend on the pending list. Once the flow ends its
// registry entry is gone, so a pending server starts a new one.
func TestMCPAuthReusesTheRunningFlow(t *testing.T) {
	t.Parallel()
	fake := newAuthFake()
	fake.AuthURL = "https://auth.example/x"
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Release even when an assertion fails first: a flow that ignores its
	// context would otherwise hold the cleanup Shutdown forever.
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	g := startAuthTest(t, fake)
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	rsp.Body.Close()
	require.JSONEq(t, `{"url":"https://auth.example/x"}`, string(body))
	fake.SetMCPPending(nil) // The running flow leaves the pending state.

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	rsp.Body.Close()
	require.JSONEq(t, `{"url":"https://auth.example/x"}`, string(body))
	// The flow records its call on its own goroutine, which the request
	// does not wait for, so the count is read until it settles.
	require.Eventually(t, func() bool {
		return countCalls(fake, "MCPAuthenticate:srv") == 1
	}, time.Second, 10*time.Millisecond, "the second request reuses the running flow")

	unblock()
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "MCPAuthenticateReleased:srv:true")
	}, time.Second, 10*time.Millisecond, "the flow must still be live once the request returned")
	require.Eventually(t, func() bool { return authFlows(g) == 0 }, time.Second, 10*time.Millisecond)

	fake.SetMCPPending([]mcp.PendingAuthServer{{Name: "srv"}})
	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return countCalls(fake, "MCPAuthenticate:srv") == 2
	}, time.Second, 10*time.Millisecond)
}

func TestMCPAuthFailurePublishesWebError(t *testing.T) {
	t.Parallel()
	fake := newAuthFake()
	fake.AuthURL = "https://auth.example/x"
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Release even when an assertion fails first: a flow that ignores its
	// context would otherwise hold the cleanup Shutdown forever.
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	fake.SetMCPAuthErr(errors.New("token endpoint returned 500"))
	g := startAuthTest(t, fake)
	s := dialStream(t, g, "")
	readFrame(t, s) // Bootstrap.

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	rsp.Body.Close()

	unblock()
	f := readErrorFrame(t, s)
	var p map[string]string
	require.NoError(t, json.Unmarshal(f.Payload, &p))
	require.Equal(t, map[string]string{
		"session_id": "",
		"code":       "mcp_auth_failed",
		"error":      "The MCP server could not be authorised. The terminal log has the details.",
	}, p)
}

// TestMCPAuthCancelledOnShutdown pins that shutdown cancels a running flow
// and that the cancellation is not reported as a failure.
func TestMCPAuthCancelledOnShutdown(t *testing.T) {
	t.Parallel()
	fake := newAuthFake()
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Release even when an assertion fails first: a flow that ignores its
	// context would otherwise hold the cleanup Shutdown forever.
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	g := startAuthTest(t, fake)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	require.NoError(t, g.Shutdown(context.Background()))
	require.Contains(t, fake.CallLog(), "MCPAuthenticateCanceled:srv")
	unblock()
}

// TestShutdownBoundsTheMCPAuthWait covers a flow that ignores its context:
// Shutdown gives up after the caller's deadline instead of hanging on it.
func TestShutdownBoundsTheMCPAuthWait(t *testing.T) {
	t.Parallel()
	fake := newAuthFake()
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Release even when an assertion fails first: this flow ignores its
	// context, so an unclosed channel would hold the cleanup Shutdown
	// forever.
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	fake.SetMCPAuthIgnoreCtx(true)
	g := startAuthTest(t, fake)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/srv/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(shutdownCtx) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown waited on an auth flow that ignores cancellation")
	}
	unblock()
}

// waitAuthFlows waits for every OAuth flow goroutine to return, bounded so
// a flow that ignores its release cannot hang the test.
func waitAuthFlows(t *testing.T, g *Gateway) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		g.authWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an OAuth flow did not return")
	}
}

// TestMCPAuthCancel pins the cancel route: it stops the running flow, a
// second cancel answers no_auth_flow, and a cancelled flow that fails
// anyway publishes nothing, because only its cancelled context keeps the
// failure off the hub.
func TestMCPAuthCancel(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{"notes": {Name: "notes", State: mcp.StateError}}
	fake.SetMCPPending([]mcp.PendingAuthServer{{Name: "notes"}})
	fake.AuthURL = "https://auth.example/x"
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	// The flow ignores the cancellation and then fails: the context guard,
	// not the fake's return value, is what must keep the failure private.
	fake.SetMCPAuthIgnoreCtx(true)
	fake.SetMCPAuthErr(errors.New("token endpoint returned 500"))
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.True(t, g.mcpAuthRunning("notes"))

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth_cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.False(t, g.mcpAuthRunning("notes"), "a cancelled flow is no longer running")

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth_cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "no_auth_flow")

	unblock()
	waitAuthFlows(t, g)
	for _, fr := range g.hub.frames() {
		require.NotEqual(t, frameError, fr.Type, "a cancelled flow publishes no failure")
	}
}

// TestMCPAuthCancelAllowsRetry pins the cancel-then-retry window: once a
// flow is cancelled it no longer counts as running, so a new auth starts a
// fresh flow even while the cancelled goroutine is still alive.
func TestMCPAuthCancelAllowsRetry(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.MCP = map[string]mcp.ClientInfo{"notes": {Name: "notes", State: mcp.StateError}}
	fake.SetMCPPending([]mcp.PendingAuthServer{{Name: "notes"}})
	fake.AuthURL = "https://auth.example/x"
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fake.SetMCPAuthRelease(release)
	// The cancelled flow ignores its context, so it is still alive when the
	// retry starts.
	fake.SetMCPAuthIgnoreCtx(true)
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth_cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.False(t, g.mcpAuthRunning("notes"))

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return countCalls(fake, "MCPAuthenticate:notes") == 2
	}, time.Second, 10*time.Millisecond, "a cancelled flow must not block a new sign-in")
	require.True(t, g.mcpAuthRunning("notes"), "the new flow owns the registry entry")

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/notes/auth_cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.False(t, g.mcpAuthRunning("notes"))

	unblock()
	waitAuthFlows(t, g)
}

// TestStaleMCPAuthFlowKeepsTheNewerOne pins the deferred cleanup: a flow
// cancelled before a retry must not remove the flow that replaced it.
func TestStaleMCPAuthFlowKeepsTheNewerOne(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	stale := &mcpAuthFlow{cancel: func() {}}
	fresh := &mcpAuthFlow{cancel: func() {}}
	g.authMu.Lock()
	g.auths["notes"] = fresh
	g.authMu.Unlock()

	g.finishMCPAuth("notes", stale)
	require.True(t, g.mcpAuthRunning("notes"), "a stale flow must not delete the newer one")
}

func TestLSPListAndDiagnostics(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.LSP = map[string]workspace.LSPClientInfo{
		"gopls": {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 3},
		"tsls":  {Name: "tsls", State: lsp.StateError, Error: errors.New("spawn failed")},
	}
	fake.LSPDiagnostics = map[string]lsp.DiagnosticCounts{
		"gopls": {Error: 2, Warning: 1, Information: 1, Hint: 4},
	}
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/lsp", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var out struct {
		Servers []lspServerInfo `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, []lspServerInfo{
		{Name: "gopls", State: "2", Diagnostics: lspDiagnostics{Error: 2, Warning: 1, Information: 1, Hint: 4}},
		{Name: "tsls", State: "3", Error: "spawn failed"},
	}, out.Servers)

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/lsp/gopls/diagnostics", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"error":2,"warning":1,"information":1,"hint":4}`, string(body))

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/lsp/ghost/diagnostics", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_server")
}

func TestLSPStartRejectsEscapingPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		path   string
		want   string
		status int
	}{
		{"parent", "../../etc", "", http.StatusBadRequest},
		{"absolute", "/etc", "", http.StatusBadRequest},
		{"climb back", "a/../../b", "", http.StatusBadRequest},
		{"inside", "internal/web", "/tmp/project/internal/web", http.StatusNoContent},
		{"working dir", "", "/tmp/project", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startTest(t)
			body := "{}"
			if tc.path != "" {
				body = fmt.Sprintf(`{"path":%q}`, tc.path)
			}
			rsp, respBody := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/lsp/start", body)
			rsp.Body.Close()
			require.Equal(t, tc.status, rsp.StatusCode, string(respBody))
			if tc.status != http.StatusNoContent {
				require.Contains(t, string(respBody), "bad_path")
				require.Empty(t, fake.CallLog(), "a refused path never reaches the LSP manager")
				return
			}
			require.Eventually(t, func() bool {
				return slices.Contains(fake.CallLog(), "LSPStart:"+tc.want)
			}, time.Second, 10*time.Millisecond)
		})
	}
}

// TestLSPStartOutlivesTheRequest pins the detached context: the servers a
// browser request starts must not be cancelled when the request ends, the
// way the TUI's start runs on a background context.
func TestLSPStartOutlivesTheRequest(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	started := make(chan context.Context, 1)
	fake.OnLSPStart = func(ctx context.Context, _ string) { started <- ctx }
	g := startFake(t, fake)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/lsp/start", "{}")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))

	var ctx context.Context
	select {
	case ctx = <-started:
	case <-time.After(time.Second):
		t.Fatal("LSPStart was not called")
	}
	// The response is fully read, so a context tied to the request would be
	// cancelled by now.
	require.Never(t, func() bool { return ctx.Err() != nil }, 100*time.Millisecond, 10*time.Millisecond)
}

// TestLSPStartKeepsOneDrain pins the queue: one goroutine runs starts at a
// time, a request for a path already running or queued adds nothing, and a
// request for another path is queued and run by that same goroutine,
// because the manager's Start filters its servers by path.
func TestLSPStartKeepsOneDrain(t *testing.T) {
	t.Parallel()

	// runningStart starts a gateway whose start for a.go blocks until the
	// returned func releases it.
	runningStart := func(t *testing.T) (*Gateway, *webtest.Fake, func()) {
		t.Helper()
		fake := webtest.NewFake()
		entered := make(chan struct{})
		release := make(chan struct{})
		var enteredOnce, releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		first := filepath.Join(fake.WorkDir, "a.go")
		fake.OnLSPStart = func(_ context.Context, path string) {
			enteredOnce.Do(func() { close(entered) })
			if path == first {
				<-release
			}
		}
		g := startFake(t, fake)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/lsp/start", `{"path":"a.go"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("the first start never reached the LSP manager")
		}
		return g, fake, unblock
	}

	t.Run("same path", func(t *testing.T) {
		t.Parallel()
		g, fake, unblock := runningStart(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/lsp/start", `{"path":"a.go"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
		unblock()
		a := filepath.Join(fake.WorkDir, "a.go")
		require.Eventually(t, func() bool {
			return countCalls(fake, "LSPStart:"+a) == 1
		}, time.Second, 10*time.Millisecond, "a path already running is not started twice")
		require.Equal(t, 1, countCalls(fake, "LSPStart:"+a))
	})

	t.Run("another path is queued", func(t *testing.T) {
		t.Parallel()
		g, fake, unblock := runningStart(t)
		c := authedClient(t, g)
		for range 2 {
			rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/lsp/start", `{"path":"b.py"}`)
			rsp.Body.Close()
			require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
		}
		b := filepath.Join(fake.WorkDir, "b.py")
		require.Zero(t, countCalls(fake, "LSPStart:"+b), "the queued path waits for the drain")
		unblock()
		require.Eventually(t, func() bool {
			return countCalls(fake, "LSPStart:"+b) == 1
		}, 2*time.Second, 10*time.Millisecond, "the drain runs a different path after the first")
		require.Equal(t, 1, countCalls(fake, "LSPStart:"+b), "a queued path is queued once")
	})
}

// TestShutdownWaitsForTheLSPStart covers both halves of the wait: an
// in-flight start and the paths queued behind it hold Shutdown until they
// settle, and a start that ignores the gateway's cancellation cannot hold
// it past the caller's deadline.
func TestShutdownWaitsForTheLSPStart(t *testing.T) {
	t.Parallel()

	// blockedStart starts a gateway whose start for a.go blocks until the
	// returned func releases it, and queues b.py behind it.
	blockedStart := func(t *testing.T) (*Gateway, *webtest.Fake, func()) {
		t.Helper()
		fake := webtest.NewFake()
		entered := make(chan struct{})
		release := make(chan struct{})
		var enteredOnce, releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		first := filepath.Join(fake.WorkDir, "a.go")
		fake.OnLSPStart = func(_ context.Context, path string) {
			enteredOnce.Do(func() { close(entered) })
			if path == first {
				<-release
			}
		}
		g := startFake(t, fake)
		c := authedClient(t, g)
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/lsp/start", `{"path":"a.go"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("the start never reached the LSP manager")
		}
		rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/lsp/start", `{"path":"b.py"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
		return g, fake, unblock
	}

	t.Run("waits for the start and drops the queue", func(t *testing.T) {
		t.Parallel()
		g, fake, unblock := blockedStart(t)
		done := make(chan error, 1)
		go func() { done <- g.Shutdown(context.Background()) }()
		returned := func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}
		require.Never(t, returned, 150*time.Millisecond, 10*time.Millisecond,
			"Shutdown returned while a start was still in flight")
		unblock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Shutdown did not return after the start settled")
		}
		require.Zero(t, countCalls(fake, "LSPStart:"+filepath.Join(fake.WorkDir, "b.py")),
			"a path queued when Shutdown began is dropped, never started")
	})

	t.Run("bounded by the deadline", func(t *testing.T) {
		t.Parallel()
		g, _, unblock := blockedStart(t)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- g.Shutdown(shutdownCtx) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Shutdown waited on an LSP start that ignores cancellation")
		}
		unblock()
	})
}

// TestLSPStartAfterShutdown pins the closing answer: once the gateway has
// shut down, a start request is refused with the shutting-down state and
// reaches no workspace call.
func TestLSPStartAfterShutdown(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	require.NoError(t, g.Shutdown(context.Background()))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/lsp/start", strings.NewReader(`{"path":"a.go"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleLSPStart(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "shutting_down")
	require.Empty(t, fake.CallLog(), "a start after shutdown reaches no workspace call")
}

func TestLSPStop(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/lsp/stop", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "LSPStopAll")
	}, time.Second, 10*time.Millisecond)
}

func TestConfigReload(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/reload", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "ReloadConfig")
	}, time.Second, 10*time.Millisecond)
}

func TestConfigReloadErrors(t *testing.T) {
	t.Parallel()
	t.Run("busy agent", func(t *testing.T) {
		t.Parallel()
		g, fake := startTest(t)
		fake.SetReloadErr(app.ErrConfigReloadBusy)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/reload", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
		require.Contains(t, string(body), "agent_busy")
	})
	t.Run("starting agent", func(t *testing.T) {
		t.Parallel()
		g, fake := startTest(t)
		fake.SetReloadErr(fmt.Errorf("reload: %w", workspace.ErrAgentNotInitialized))
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/reload", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
		require.Contains(t, string(body), "agent_starting")
	})
}

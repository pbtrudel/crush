package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func startTest(t *testing.T) (*Gateway, *webtest.Fake) {
	t.Helper()
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	// t.Context is already cancelled when cleanups run, which would make
	// Shutdown return without draining.
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g, fake
}

// do sends a request bound to the test context. A nil client means
// http.DefaultClient. The caller must close the response body.
func do(t *testing.T, c *http.Client, method, target string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	require.NoError(t, err)
	return doReq(t, c, req)
}

// doReq sends a prepared request; use it when headers or Host must be set.
func doReq(t *testing.T, c *http.Client, req *http.Request) *http.Response {
	t.Helper()
	if c == nil {
		c = http.DefaultClient
	}
	rsp, err := c.Do(req)
	require.NoError(t, err)
	return rsp
}

func authedClient(t *testing.T, g *Gateway) *http.Client {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth",
		strings.NewReader(`{"token":"`+g.token()+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", g.URL())
	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	cookies := rsp.Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	require.Equal(t, "crush_web", c.Name)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
	jar := &staticJar{c: c}
	return &http.Client{Jar: jar}
}

type staticJar struct{ c *http.Cookie }

func (j *staticJar) SetCookies(*url.URL, []*http.Cookie) {}
func (j *staticJar) Cookies(*url.URL) []*http.Cookie     { return []*http.Cookie{j.c} }

func TestStartBindsLoopbackOnly(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	u, err := url.Parse(g.URL())
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", u.Hostname())
	require.Contains(t, g.LaunchURL(), "/#t="+g.token())
	require.Len(t, g.token(), 43)
}

func TestAuthWrongTokenRejected(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth",
		strings.NewReader(`{"token":"nope"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	rsp := doReq(t, nil, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}

func TestAPIRequiresCookie(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/api/health", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)

	rsp = do(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/health", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
}

func TestAuthRejectsForeignOriginAndHost(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL()+"/api/health", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.example")
	rsp := doReq(t, c, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)

	// A bad Host on an API path answers with the JSON error shape.
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL()+"/api/health", nil)
	require.NoError(t, err)
	req.Host = "attacker.example"
	rsp = doReq(t, c, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
	require.Equal(t, "application/json", rsp.Header.Get("Content-Type"))
	raw, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"error":"host not allowed","code":"forbidden_host"}`, string(raw))

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL()+"/", nil)
	require.NoError(t, err)
	req.Host = "attacker.example"
	rsp = doReq(t, nil, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
}

func TestStaticServedWithoutAuthAndHeaders(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Security-Policy"), "default-src 'self'")
	require.Contains(t, rsp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.Equal(t, "nosniff", rsp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "no-referrer", rsp.Header.Get("Referrer-Policy"))
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
}

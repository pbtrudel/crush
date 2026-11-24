package web

import (
	"cmp"
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	// Browsers send Origin on every state-changing request, and the
	// gateway requires it.
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", (&url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host}).String())
	}
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

// bearer adds the gateway token to every request, the way the page does.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
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
	require.Empty(t, rsp.Cookies(), "the gateway sets no cookie")
	return &http.Client{Transport: bearer{token: g.token(), next: http.DefaultTransport}}
}

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
	req.Header.Set("Origin", g.URL())
	rsp := doReq(t, nil, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}

func TestAPIRequiresBearerToken(t *testing.T) {
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

func TestTwoGatewaysRefuseEachOthersTokens(t *testing.T) {
	t.Parallel()
	a, _ := startTest(t)
	b, _ := startTest(t)
	rsp := do(t, authedClient(t, a), http.MethodGet, b.URL()+"/api/health", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}

func TestTokenInQueryIsIgnored(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, q := range []string{"?token=", "?t=", "?access_token="} {
		rsp := do(t, nil, http.MethodGet, g.URL()+"/api/health"+q+g.token(), nil)
		rsp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, rsp.StatusCode, q)
	}
}

func TestCookieNoLongerAuthenticates(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL()+"/api/health", nil)
	require.NoError(t, err)
	req.Header.Set("Cookie", "crush_web_"+strconv.Itoa(g.port)+"="+g.token())
	rsp := doReq(t, nil, req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}

func TestAssetDirectoryPathsAreNotFound(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	// The build output contains an assets/ directory; it must not become a
	// file listing.
	rsp := do(t, nil, http.MethodGet, g.URL()+"/assets/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), "<a href")

	rsp = do(t, nil, http.MethodGet, g.URL()+"/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
}

func TestAPIResponsesAreNotStored(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/health", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, "no-store", rsp.Header.Get("Cache-Control"))
}

// routeParam matches a mux path placeholder such as {sid}.
var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// TestEveryRouteRequiresTheToken walks the gateway's own route registry,
// so a route cannot ship without proving it refuses a request that has no
// token. The registry is filled by g.route, which every guarded /api
// route uses.
func TestEveryRouteRequiresTheToken(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	require.NotEmpty(t, g.apiRoutes)
	for _, pattern := range g.apiRoutes {
		method, path, ok := strings.Cut(pattern, " ")
		require.True(t, ok, "route %q has no method", pattern)
		path = routeParam.ReplaceAllString(path, "x")
		req, err := http.NewRequestWithContext(t.Context(), method, g.URL()+path, strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Origin", g.URL())
		rsp := doReq(t, nil, req)
		rsp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, rsp.StatusCode, "%s %s", method, path)
	}
	// The login route must answer without a token, so it is not in the
	// walk and must never be added to it.
	require.NotContains(t, g.apiRoutes, "POST /api/auth")
	// The session routes added with the registry are part of the walk.
	for _, want := range []string{
		"PUT /api/sessions/{sid}",
		"DELETE /api/sessions/{sid}",
		"POST /api/follow",
		"GET /api/commands",
		"POST /api/commands/run",
		"POST /api/mcp/prompts/run",
		"POST /api/skills/{name}/attach",
		"POST /api/sessions/{sid}/summarize",
		"POST /api/sessions/{sid}/ask",
		"POST /api/sessions/{sid}/shell",
		"DELETE /api/shell/{run}",
		"POST /api/sessions/{sid}/notes",
		"GET /api/providers",
		"POST /api/models/select",
		"POST /api/models/reasoning",
		"GET /api/mcp",
		"POST /api/mcp/{name}/{action}",
		"GET /api/lsp",
		"GET /api/lsp/{name}/diagnostics",
		"POST /api/lsp/start",
		"POST /api/lsp/stop",
		"POST /api/config/reload",
		"POST /api/plan/handoff",
		"GET /api/auth/providers",
		"POST /api/auth/provider/key",
	} {
		require.Contains(t, g.apiRoutes, want)
	}
}

func TestStateChangingRequestsNeedAnOrigin(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/cancel", nil)
	require.NoError(t, err)
	rsp := doReq(t, c, req)
	rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
	require.NotContains(t, fake.CallLog(), "AgentCancel:s1")

	// Reads stay open to same-host tools without an Origin.
	rsp = do(t, c, http.MethodGet, g.URL()+"/api/health", nil)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
}

func TestMissingTokenWithForeignOriginIsForbidden(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/cancel", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://evil.example")
	rsp := doReq(t, nil, req)
	rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
}

func TestAuthRejectsForeignOriginOnTokenExchange(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth",
		strings.NewReader(`{"token":"`+g.token()+`"}`))
	require.NoError(t, err)
	req.Header.Set("Origin", "http://evil.example")
	rsp := doReq(t, nil, req)
	rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
	require.Empty(t, rsp.Cookies())
}

// TestOriginAllowed pins the matrix behind the Origin guard: a state
// changing request needs the tab's own origin, a read may come from a
// same-host tool without one, and neither scheme, port nor host name may
// differ from the address the gateway bound.
func TestOriginAllowed(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	port := strconv.Itoa(g.port)
	otherPort := strconv.Itoa(g.port + 1)
	same := "http://127.0.0.1:" + port
	localhost := "http://localhost:" + port
	for _, tc := range []struct {
		name   string
		method string
		host   string
		origin string
		want   bool
	}{
		{"read without an Origin", http.MethodGet, "", "", true},
		{"head without an Origin", http.MethodHead, "", "", true},
		{"post without an Origin", http.MethodPost, "", "", false},
		{"the bound origin", http.MethodPost, "", same, true},
		{"the localhost origin for 127.0.0.1", http.MethodGet, "", localhost, false},
		{"the loopback origin for localhost", http.MethodGet, "localhost:" + port, same, false},
		{"the origin the request was sent to", http.MethodGet, "localhost:" + port, localhost, true},
		{"a null Origin", http.MethodPost, "", "null", false},
		{"an https origin", http.MethodPost, "", "https://127.0.0.1:" + port, false},
		{"a wrong port", http.MethodPost, "", "http://127.0.0.1:" + otherPort, false},
		{"a foreign host", http.MethodPost, "", "http://evil.example", false},
		{"an unparseable origin", http.MethodPost, "", "http://[::1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), tc.method, "/api/health", nil)
			require.NoError(t, err)
			req.Host = cmp.Or(tc.host, "127.0.0.1:"+port)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			require.Equal(t, tc.want, g.originAllowed(req))
		})
	}
}

func TestMissingAssetIsNotCachedForever(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/assets/missing-0000.js", nil)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.NotContains(t, rsp.Header.Get("Cache-Control"), "immutable")
	// net/http already strips Cache-Control from a file-server error, so
	// only the explicit header pins that this handler sets it.
	require.Equal(t, "no-store", rsp.Header.Get("Cache-Control"))
}

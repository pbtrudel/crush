package web

import (
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootServesTheShell pins the page a tab gets: the built index, with
// no inline script or style, because the CSP allows self only.
func TestRootServesTheShell(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "text/html")
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	page := string(body)
	require.Contains(t, page, `<div id="root">`)
	require.Contains(t, page, `/assets/`)
	require.NotContains(t, page, "<script>", "the CSP allows no inline script")
	require.NotContains(t, page, `style="`, "the CSP allows no inline style")
}

// TestShellAnswersAnyPath pins the deep-link case.
func TestShellAnswersAnyPath(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, path := range []string{"/sessions/s1", "/settings/appearance"} {
		rsp := do(t, nil, http.MethodGet, g.URL()+path, nil)
		body, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rsp.StatusCode, path)
		require.Contains(t, string(body), `<div id="root">`, path)
	}
}

// TestHashedAssetIsImmutable pins the cache header the entry bundle needs.
func TestHashedAssetIsImmutable(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	entry := builtEntry(t, g.URL())
	rsp := do(t, nil, http.MethodGet, g.URL()+entry, nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, "public, max-age=31536000, immutable", rsp.Header.Get("Cache-Control"))
}

// builtEntry returns the first /assets/ script the served page loads.
func builtEntry(t *testing.T, origin string) string {
	t.Helper()
	rsp := do(t, nil, http.MethodGet, origin+"/", nil)
	defer rsp.Body.Close()
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	m := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(string(body))
	require.Len(t, m, 2, "the built page loads no hashed script")
	return m[1]
}

// TestStaleHashedAssetIsNotFoundAndNotCached pins the cached-tab case: a
// bundle the old build shipped is gone, so it must answer 404 and must not
// carry the year-long immutable header a real asset gets.
func TestStaleHashedAssetIsNotFoundAndNotCached(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/assets/index-43JjnKFV.js", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Equal(t, "no-store", rsp.Header.Get("Cache-Control"))
}

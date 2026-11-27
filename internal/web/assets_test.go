package web

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootServesThePlaceholderPage pins the page a tab gets while the new
// UI is being built. The CSP allows no inline script and no inline style,
// so either one would leave the tab blank.
func TestRootServesThePlaceholderPage(t *testing.T) {
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
	require.Contains(t, page, "being rebuilt")
	require.Contains(t, page, `href="/style.css"`)
	require.NotContains(t, page, "<script", "the CSP allows no inline script")
	require.NotContains(t, page, `style="`, "the CSP allows no inline style")
}

// TestPlaceholderAnswersAnyPath pins the deep-link case: assetHandler
// rewrites an unknown path to "/" so a bookmark opens the page.
func TestPlaceholderAnswersAnyPath(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, path := range []string{"/sessions/s1", "/settings/appearance"} {
		rsp := do(t, nil, http.MethodGet, g.URL()+path, nil)
		body, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rsp.StatusCode, path)
		require.Contains(t, string(body), "being rebuilt", path)
	}
}

// TestPlaceholderStylesheetIsServed pins the external stylesheet the page
// loads: a 404 here means an unstyled page under the strict CSP.
func TestPlaceholderStylesheetIsServed(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/style.css", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "text/css")
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
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

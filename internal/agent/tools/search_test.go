package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// serveSearchStub points the DuckDuckGo endpoint at a local server
// returning the given status and body, and restores it on cleanup.
func serveSearchStub(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := ddgLiteEndpoint
	ddgLiteEndpoint = srv.URL + "/lite/?q="
	t.Cleanup(func() { ddgLiteEndpoint = orig })
	t.Cleanup(resetSearchBlock)
}

// loadAnomalyPage reads the real bot-check payload captured from
// lite.duckduckgo.com on 2026-07-29 (HTTP 202, captcha modal).
func loadAnomalyPage(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/ddg_anomaly_202.html")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	return string(data)
}

// TestSearchRateLimitedOn202 verifies the 202 anomaly-challenge
// interstitial is reported as throttling, not parsed into an empty
// result set.
func TestSearchRateLimitedOn202(t *testing.T) {
	serveSearchStub(t, http.StatusAccepted, loadAnomalyPage(t))
	_, err := searchDuckDuckGo(context.Background(), http.DefaultClient, "anything", 10)
	if !errors.Is(err, errSearchRateLimited) {
		t.Fatalf("expected errSearchRateLimited, got %v", err)
	}
}

// TestSearchRateLimitedOnAnomalyPage verifies the captcha modal is
// detected by content as well: DuckDuckGo also serves the same page
// with HTTP 200 once a client is flagged.
func TestSearchRateLimitedOnAnomalyPage(t *testing.T) {
	serveSearchStub(t, http.StatusOK, loadAnomalyPage(t))
	_, err := searchDuckDuckGo(context.Background(), http.DefaultClient, "anything", 10)
	if !errors.Is(err, errSearchRateLimited) {
		t.Fatalf("expected errSearchRateLimited, got %v", err)
	}
}

// TestSearchParsesNormalResults guards against false positives: a
// genuine results page still parses.
func TestSearchParsesNormalResults(t *testing.T) {
	page := `<html><body><table>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpost">Example Post</a></td></tr>
<tr><td class="result-snippet">A snippet about the example post.</td></tr>
</table></body></html>`
	serveSearchStub(t, http.StatusOK, page)
	results, err := searchDuckDuckGo(context.Background(), http.DefaultClient, "example", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Link != "https://example.com/post" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

// A block lasts for the configured period and costs one request, not one per
// caller: the second search in the window never reaches the network.
func TestSearchBlockSkipsTheNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	orig := ddgLiteEndpoint
	ddgLiteEndpoint = srv.URL + "/lite/?q="
	t.Cleanup(func() { ddgLiteEndpoint = orig })

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	origNow := searchNow
	searchNow = func() time.Time { return now }
	t.Cleanup(func() {
		searchNow = origNow
		resetSearchBlock()
	})

	_, err := searchDuckDuckGo(t.Context(), srv.Client(), "q", 5)
	require.ErrorIs(t, err, errSearchRateLimited)
	require.Equal(t, int32(1), hits.Load())
	require.True(t, SearchBlocked())

	_, err = searchDuckDuckGo(t.Context(), srv.Client(), "q", 5)
	require.ErrorIs(t, err, errSearchRateLimited)
	require.Equal(t, int32(1), hits.Load(), "a blocked search must not reach the network again")

	now = now.Add(searchBlockedFor - time.Minute)
	require.True(t, SearchBlocked(), "the block lasts the whole period")

	now = now.Add(2 * time.Minute)
	require.False(t, SearchBlocked())
	_, err = searchDuckDuckGo(t.Context(), srv.Client(), "q", 5)
	require.ErrorIs(t, err, errSearchRateLimited)
	require.Equal(t, int32(2), hits.Load(), "after the block expires the next search tries again")
}

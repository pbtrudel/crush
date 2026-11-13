package copilot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchCreditsUsed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/copilot_internal/user", r.URL.Path)
		require.Equal(t, "token gh-token", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		require.Equal(t, userAgent, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{"quota_snapshots":{"premium_interactions":{"credits_used":2567.6,"unlimited":true},"chat":{}}}`))
	}))
	t.Cleanup(srv.Close)

	used, err := fetchCreditsUsed(t.Context(), srv.URL, "gh-token")
	require.NoError(t, err)
	require.Equal(t, 2568, used)
}

func TestFetchCreditsUsedNon200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := fetchCreditsUsed(t.Context(), srv.URL, "gh-token")
	require.ErrorContains(t, err, "401")
}

func TestFetchCreditsUsedMissingSnapshot(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"quota_snapshots":{"chat":{"unlimited":true}}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := fetchCreditsUsed(t.Context(), srv.URL, "gh-token")
	require.ErrorContains(t, err, "premium_interactions")
}

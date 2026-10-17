package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReloadSkillsPostsReload is the endpoint-shape regression test for
// the "Reload Skills" command: a POST to the workspace's reload route.
func TestReloadSkillsPostsReload(t *testing.T) {
	t.Parallel()

	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.ReloadSkills(context.Background(), "ws1"))

	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "/v1/workspaces/ws1/skills/reload", path)
}

// TestReloadSkillsPropagatesErrorStatus verifies a non-200 response
// surfaces as an error instead of a silent no-op.
func TestReloadSkillsPropagatesErrorStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.Error(t, c.ReloadSkills(context.Background(), "ws1"))
}

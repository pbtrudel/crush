package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestListAllSkillsPath pins the SDK route for the full skill set: a GET
// to the workspace's /skills/all route, decoded into proto.SkillInfo.
func TestListAllSkillsPath(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method, path = r.Method, r.URL.Path
		mu.Unlock()
		_, _ = w.Write([]byte(`[{"id":"b","name":"crush-config"}]`))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	got, err := c.ListAllSkills(context.Background(), "ws1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].ID)
	require.Equal(t, "crush-config", got[0].Name)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, http.MethodGet, method)
	require.Equal(t, "/v1/workspaces/ws1/skills/all", path)
}

// TestListAllSkillsPropagatesErrorStatus verifies the SDK mirrors
// ListSkills' bare status check: a non-200 answer is an error, not a
// catalog decoded from whatever body came with it.
func TestListAllSkillsPropagatesErrorStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.ListAllSkills(context.Background(), "ws1")
	require.Error(t, err)
}

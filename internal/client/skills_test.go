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

// TestListAllSkillsPropagatesErrorStatus verifies a non-200 answer is an
// error, not a catalog decoded from whatever body came with it.
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

// TestSkillListServerErrorCarriesMessage pins that both list calls decode
// the server's error body, the way the rest of the SDK reports a failed
// call, instead of discarding the message.
func TestSkillListServerErrorCarriesMessage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"catalog unavailable"}`))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.ListSkills(context.Background(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
	require.Contains(t, err.Error(), "catalog unavailable")
	require.NotErrorIs(t, err, ErrNotFound)

	_, err = c.ListAllSkills(context.Background(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
	require.Contains(t, err.Error(), "catalog unavailable")
	require.NotErrorIs(t, err, ErrNotFound)
}

// TestListSkillsMapsNotFound pins the shared status mapping: a 404 from a
// workspace the server no longer knows is ErrNotFound, carrying the
// server's message, so the workspace layer can treat it as gone.
func TestListSkillsMapsNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"workspace not found"}`))
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ListSkills(context.Background(), "ws1")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, err.Error(), "workspace not found")
}

// TestListAllSkillsMapsNotFound pins the same 404 mapping for the full-set
// route: a JSON error body means the server knows the route and has lost
// the workspace.
func TestListAllSkillsMapsNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"workspace not found"}`))
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ListAllSkills(context.Background(), "ws1")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, err.Error(), "workspace not found")
}

// TestListAllSkillsMissingRouteIsUnsupported pins the older-server answer:
// a 404 with no named server error is Go's mux replying to a route the
// server does not have, or an intermediary answering in its place. It must
// read as unsupported so a caller can fall back to the visible set instead
// of treating the workspace as gone.
func TestListAllSkillsMissingRouteIsUnsupported(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{"plain text", "404 page not found\n"},
		{"empty object", "{}"},
		{"empty body", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := captureClient(t, srv).ListAllSkills(context.Background(), "ws1")
			require.ErrorIs(t, err, ErrUnsupported)
			require.NotErrorIs(t, err, ErrNotFound)
			require.Contains(t, err.Error(), "status code 404")
		})
	}
}

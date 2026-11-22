package multiturn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestServerTransportDrivesOneSession checks what the API path has to get
// right: the workspace is opened for a named client, the agent is initialized
// interactively (a non-interactive coordinator never starts a background
// summary), the event stream is held open so the workspace is not torn down,
// and a prompt waits for the run instead of returning as soon as it is
// accepted.
func TestServerTransportDrivesOneSession(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		bodies  = map[string]string{}
		busy    = 0
		streams = 0
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("POST /v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		body := capture(t, r, bodies, "/v1/workspaces")
		var req struct {
			Path           string `json:"path"`
			ClientID       string `json:"client_id"`
			PermissionMode string `json:"permission_mode"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		require.Equal(t, harnessClientID, req.ClientID)
		require.Equal(t, "yolo", req.PermissionMode)
		w.Write([]byte(`{"id":"ws1"}`))
	})
	mux.HandleFunc("POST /v1/workspaces/ws1/agent/init", func(w http.ResponseWriter, r *http.Request) {
		capture(t, r, bodies, "/agent/init")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1/workspaces/ws1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		streams++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /v1/workspaces/ws1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"s1"}`))
	})
	mux.HandleFunc("POST /v1/workspaces/ws1/agent", func(w http.ResponseWriter, r *http.Request) {
		capture(t, r, bodies, "/agent")
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /v1/workspaces/ws1/agent/sessions/s1", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		busy++
		// The run starts on the second poll and finishes two polls later,
		// so the driver has to wait for both edges.
		running := busy >= 2 && busy < 4
		mu.Unlock()
		if running {
			w.Write([]byte(`{"id":"s1","is_busy":true}`))
			return
		}
		w.Write([]byte(`{"id":"s1","is_busy":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := &serverTransport{base: srv.URL, client: &http.Client{Timeout: 5 * time.Second}, stream: &http.Client{}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	require.NoError(t, s.createWorkspace(ctx, "/tmp/checkout"))
	require.NoError(t, s.initAgent(ctx))
	require.NoError(t, s.attach(ctx))
	require.NoError(t, s.createSession(ctx))
	require.Equal(t, "ws1", s.wsID)
	require.Equal(t, "s1", s.sessID)

	wall, idle, err := s.prompt(ctx, "fix the bug", 10*time.Second)
	require.NoError(t, err)
	require.Positive(t, wall)
	require.Positive(t, idle)
	require.GreaterOrEqual(t, wall, idle)

	mu.Lock()
	initBody := bodies["/agent/init"]
	promptBody := bodies["/agent"]
	opened := streams
	mu.Unlock()
	require.Contains(t, initBody, `"interactive":true`)
	require.Contains(t, promptBody, `"session_id":"s1"`)
	require.Contains(t, promptBody, "fix the bug")
	require.Equal(t, 1, opened)

	s.close()
}

// capture records a request body under key and returns it.
func capture(t *testing.T, r *http.Request, into map[string]string, key string) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	body := sb.String()
	into[key] = body
	return body
}

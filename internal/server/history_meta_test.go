package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/server"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// historyHarness is the smallest server the SDK can talk to: a real
// Server over an httptest listener, with a synthetic workspace whose App
// carries real session and history services. It lives in the external
// test package because the client SDK imports the server package.
type historyHarness struct {
	httpSrv     *httptest.Server
	workspaceID string
	sessions    session.Service
	history     history.Service
	// app is the workspace's App, so a test can swap a service.
	app *app.App
	// logs holds what the server logged.
	logs *bytes.Buffer
}

func newHistoryHarness(t *testing.T) *historyHarness {
	t.Helper()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)

	srv := server.NewServer(nil, "tcp", "127.0.0.1:0")
	logs := new(bytes.Buffer)
	srv.SetLogger(slog.New(slog.NewTextHandler(logs, nil)))
	ws := &backend.Workspace{ID: "ws-history", Path: t.TempDir(), App: a}
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})
	backend.InsertWorkspaceForTest(srv.Backend(), ws)

	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
	})
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	a.Sessions = session.NewService(q, conn)
	a.History = history.NewService(q, conn)

	return &historyHarness{
		httpSrv:     hs,
		workspaceID: ws.ID,
		sessions:    a.Sessions,
		history:     a.History,
		app:         a,
		logs:        logs,
	}
}

// newClient returns an SDK client for the harness server.
func (h *historyHarness) newClient(t *testing.T) *client.Client {
	t.Helper()
	u, err := url.Parse(h.httpSrv.URL)
	require.NoError(t, err)
	cl, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return cl
}

// metasJSONContent GETs the metadata route raw and returns any content
// key it finds. The meta route never sends content.
func (h *historyHarness) metasJSONContent(t *testing.T, sid string) any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspaceID+"/sessions/"+sid+"/history/meta", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raws []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raws))
	for _, raw := range raws {
		if content, ok := raw["content"]; ok {
			return content
		}
	}
	return nil
}

// TestHistoryMetaRoute pins the metadata wire contract: every field but
// content is present, size counts bytes, and the by-id route answers
// the full version or 404.
func TestHistoryMetaRoute(t *testing.T) {
	t.Parallel()
	h := newHistoryHarness(t)
	sess, err := h.sessions.Create(t.Context(), "history route")
	require.NoError(t, err)
	_, err = h.history.Create(t.Context(), sess.ID, "/repo/a.go", "v1")
	require.NoError(t, err)
	_, err = h.history.CreateVersion(t.Context(), sess.ID, "/repo/a.go", "v2")
	require.NoError(t, err)

	cl := h.newClient(t)
	metas, err := cl.ListSessionHistoryMeta(t.Context(), h.workspaceID, sess.ID)
	require.NoError(t, err)
	require.Len(t, metas, 2)
	require.Empty(t, h.metasJSONContent(t, sess.ID), "the meta route never sends content")
	for i, meta := range metas {
		require.NotEmpty(t, meta.ID)
		require.Equal(t, sess.ID, meta.SessionID)
		require.Equal(t, "/repo/a.go", meta.Path)
		require.Equal(t, int64(i), meta.Version)
		require.Equal(t, int64(2), meta.Size)
		require.Positive(t, meta.CreatedAt)
		require.Positive(t, meta.UpdatedAt)
	}

	f, err := cl.GetHistoryFile(t.Context(), h.workspaceID, metas[1].ID)
	require.NoError(t, err)
	require.Equal(t, "v2", f.Content)
	require.Equal(t, "/repo/a.go", f.Path)
	require.Equal(t, int64(1), f.Version)

	_, err = cl.GetHistoryFile(t.Context(), h.workspaceID, "no-such-file")
	require.ErrorIs(t, err, client.ErrNotFound)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspaceID+"/history/no-such-file", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	var apiErr proto.Error
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
	require.Equal(t, "file version not found", apiErr.Message)
}

// TestChildSessionsRoute pins the children route and the backend method
// behind it, which had no direct test.
func TestChildSessionsRoute(t *testing.T) {
	t.Parallel()
	h := newHistoryHarness(t)
	parent, err := h.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	child, err := h.sessions.CreateTaskSession(t.Context(), "tool-call-1", parent.ID, "child")
	require.NoError(t, err)

	cl := h.newClient(t)
	children, err := cl.ListChildSessions(t.Context(), h.workspaceID, parent.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, child.ID, children[0].ID)
	require.Equal(t, parent.ID, children[0].ParentSessionID)

	children, err = cl.ListChildSessions(t.Context(), h.workspaceID, "no-such-session")
	require.NoError(t, err)
	require.Empty(t, children)
}

// failingHistory fails the two reads behind the history routes, the way a
// database that cannot open its file does.
type failingHistory struct {
	history.Service
	err error
}

func (f failingHistory) ListMetaBySession(context.Context, string) ([]history.FileMeta, error) {
	return nil, f.err
}

func (f failingHistory) Get(context.Context, string) (history.File, error) {
	return history.File{}, f.err
}

// TestHistoryRoutesHideStoreErrors pins that a store failure answers a
// fixed message: the store's error can name a database path, so it goes to
// the server log instead of the body.
func TestHistoryRoutesHideStoreErrors(t *testing.T) {
	t.Parallel()
	h := newHistoryHarness(t)
	sess, err := h.sessions.Create(t.Context(), "history errors")
	require.NoError(t, err)
	boom := errors.New("get history: open /home/user/.crush/crush.db: disk I/O error")
	h.app.History = failingHistory{err: boom}
	base := h.httpSrv.URL + "/v1/workspaces/" + h.workspaceID

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"meta", "/sessions/" + sess.ID + "/history/meta", "the file history could not be read"},
		{"by id", "/history/any-id", "the file version could not be read"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+tc.path, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode, tc.name+": "+string(body))
		require.Contains(t, string(body), tc.want, tc.name)
		require.NotContains(t, string(body), "disk I/O error", tc.name)
		require.NotContains(t, string(body), "/home/user", tc.name)
	}
	require.Contains(t, h.logs.String(), "disk I/O error", "the store error stays in the log")
}

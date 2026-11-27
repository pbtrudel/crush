package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// renameSessions is a session service that keeps titles in memory, so the
// rename route can be driven without a database.
type renameSessions struct {
	session.Service // embed nil to inherit the unexported broker methods
	mu              sync.Mutex
	titles          map[string]string
}

func (s *renameSessions) Get(_ context.Context, id string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	title, ok := s.titles[id]
	if !ok {
		return session.Session{}, sql.ErrNoRows
	}
	return session.Session{ID: id, Title: title}, nil
}

// Rename updates only a known session. Like the real service it does not
// report a miss; the read that follows does.
func (s *renameSessions) Rename(_ context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.titles[id]; ok {
		s.titles[id] = title
	}
	return nil
}

func newRenameController(t *testing.T) (*controllerV1, string) {
	t.Helper()
	b := backend.New(context.Background(), nil, nil)
	wsID := uuid.New().String()
	a := &app.App{AgentCoordinator: &stubCoordinator{}}
	a.Sessions = &renameSessions{titles: map[string]string{"s1": "Old"}}
	ws := &backend.Workspace{ID: wsID, Path: t.TempDir(), App: a}
	backend.InsertWorkspaceForTest(b, ws)
	s := &Server{backend: b}
	return &controllerV1{backend: b, server: s}, wsID
}

func postRename(t *testing.T, c *controllerV1, wsID, sid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/workspaces/"+wsID+"/sessions/"+sid+"/rename", bytes.NewReader([]byte(`{"title":"Parser work"}`)))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", sid)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceSessionRename(rec, req)
	return rec
}

func TestPostWorkspaceSessionRename(t *testing.T) {
	t.Parallel()
	c, wsID := newRenameController(t)

	rec := postRename(t, c, wsID, "s1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got proto.Session
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "s1", got.ID)
	require.Equal(t, "Parser work", got.Title)
}

// TestPostWorkspaceSessionRenameUnknownSession pins the distinct code: a
// missing session is 404 session_not_found, not the bare workspace 404
// that makes clients recover.
func TestPostWorkspaceSessionRenameUnknownSession(t *testing.T) {
	t.Parallel()
	c, wsID := newRenameController(t)

	rec := postRename(t, c, wsID, "nope")
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	var got proto.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, proto.ErrorCodeSessionNotFound, got.Code)
	require.Equal(t, "session not found", got.Message)
}

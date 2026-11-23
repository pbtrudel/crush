package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// TestRenameSessionPublishes pins that a rename answers with the session
// and reaches every open tab through the normal session frame, so a tab
// that did not send the rename shows the new title too.
func TestRenameSessionPublishes(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s1", Title: "Old"})
	c := dialStream(t, g, "")
	require.Equal(t, frameSnapshot, readFrame(t, c).Type)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPut, g.URL()+"/api/sessions/s1", `{"title":"  Parser work  "}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), `"title":"Parser work"`)

	got, err := f.GetSession(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "Parser work", got.Title)

	for {
		frame := readFrame(t, c)
		if frame.Type != "session" {
			continue
		}
		require.Contains(t, string(frame.Payload), `"title":"Parser work"`)
		break
	}
}

func TestRenameRejectsBlankTitle(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s1", Title: "Old"})
	for _, title := range []string{`"   "`, `"` + strings.Repeat("x", maxTitleRunes+1) + `"`} {
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPut, g.URL()+"/api/sessions/s1", `{"title":`+title+`}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
		require.Contains(t, string(body), "bad_title")
	}
	got, err := f.GetSession(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "Old", got.Title, "a refused rename leaves the title alone")
}

func TestRenameUnknownSessionIs404(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPut, g.URL()+"/api/sessions/nope", `{"title":"Gone"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_session")
}

func TestDeleteBusySessionConflicts(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s2", Title: "Busy"})
	f.SetSessionBusy("s2", true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodDelete, g.URL()+"/api/sessions/s2", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "session_busy")
	_, err := f.GetSession(t.Context(), "s2")
	require.NoError(t, err, "a refused delete leaves the session alone")
}

func TestDeleteCurrentSessionConflicts(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s1", Title: "Shown"})
	require.NoError(t, f.SetCurrentSession(t.Context(), "s1"))
	rsp, body := doJSON(t, authedClient(t, g), http.MethodDelete, g.URL()+"/api/sessions/s1", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "session_current")
	_, err := f.GetSession(t.Context(), "s1")
	require.NoError(t, err, "the session the terminal shows stays")
}

func TestDeleteSessionRemoves(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s3", Title: "Old work"})
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodDelete, g.URL()+"/api/sessions/s3", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	_, err := f.GetSession(t.Context(), "s3")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestDeleteUnknownSessionIs404(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodDelete, g.URL()+"/api/sessions/nope", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_session")
}

func TestFollowOpensSessionInTerminal(t *testing.T) {
	t.Parallel()
	opened := make(chan string, 1)
	g, f := startWith(t, Options{OnSessionCreated: func(id string) { opened <- id }})
	f.AddSession(session.Session{ID: "s4", Title: "Other"})
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/follow", `{"session_id":"s4"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	require.Equal(t, "s4", f.CurrentSessionID(), "the workspace reports the session the terminal will show")
	select {
	case id := <-opened:
		require.Equal(t, "s4", id)
	case <-time.After(time.Second):
		t.Fatal("the terminal was not asked to open the session")
	}
}

func TestFollowRejectsMissingSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/follow", `{}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "missing_session")
}

func TestFollowUnknownSession404(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/follow", `{"session_id":"nope"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_session")
}

// reversedSessions hands back the fake's newest-first list backwards, so
// only the gateway's own sort can produce the order TestListSessionsNewestFirst
// asserts.
type reversedSessions struct{ *webtest.Fake }

func (r reversedSessions) ListSessions(ctx context.Context) ([]session.Session, error) {
	list, err := r.Fake.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	slices.Reverse(list)
	return list, nil
}

// TestListSessionsNewestFirst pins the switcher's order: newest updated
// first, with the session ID breaking ties so the order is stable across
// requests.
func TestListSessionsNewestFirst(t *testing.T) {
	t.Parallel()
	f := webtest.NewFake()
	f.AddSession(session.Session{ID: "s2", Title: "old", UpdatedAt: 10})
	f.AddSession(session.Session{ID: "s3", Title: "new", UpdatedAt: 20})
	f.AddSession(session.Session{ID: "s0", Title: "tie", UpdatedAt: 20})
	g, err := Start(t.Context(), Options{Workspace: reversedSessions{f}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)

	var list []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &list))
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	// s1 is the fake's default session, last because nothing touched it.
	require.Equal(t, []string{"s0", "s3", "s2", "s1"}, ids)
}

// TestFollowUnknownSessionOverClientWorkspace pins the client/server path
// for a missing session: the server's session_not_found answer maps back
// to 404 unknown_session, not the generic 500.
func TestFollowUnknownSessionOverClientWorkspace(t *testing.T) {
	t.Parallel()
	g := startClientGateway(t, &teamStubCoordinator{})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/follow", `{"session_id":"nope"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_session")
}

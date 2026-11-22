package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/server"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

func writeTeam(t *testing.T, dir string, state agentteam.State) {
	t.Helper()
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "team.json"), data, 0o644))
}

func writeRuns(t *testing.T, dir string, runs []agentrun.Run) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": 1, "runs": runs})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agents.json"), data, 0o644))
}

func alice() agentteam.Member {
	return agentteam.Member{
		ID: "m-1", Name: "alice", Role: agentteam.RoleTeammate,
		SessionID: "team-m-1", ParentID: "s1", Status: agentteam.StatusRunning,
	}
}

func startWith(t *testing.T, opts Options) (*Gateway, *webtest.Fake) {
	t.Helper()
	fake := webtest.NewFake()
	opts.Workspace = fake
	g, err := Start(t.Context(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g, fake
}

func TestTeamWatcherEmitsOnlyOnChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTeam(t, dir, agentteam.State{TeamID: "team-1", Members: []agentteam.Member{alice()}})
	w := newTeamWatcher(dir)

	team, runs := w.poll()
	require.NotNil(t, team)
	require.Contains(t, string(team), `"name":"alice"`)
	require.JSONEq(t, `[]`, string(runs))

	team, runs = w.poll()
	require.Nil(t, team, "an unchanged file publishes nothing")
	require.Nil(t, runs)

	writeTeam(t, dir, agentteam.State{TeamID: "team-1"})
	team, _ = w.poll()
	require.JSONEq(t, `{"team_id":"team-1","members":[],"tasks":[]}`, string(team))
}

func TestTeamWatcherMissingFiles(t *testing.T) {
	t.Parallel()
	w := newTeamWatcher(filepath.Join(t.TempDir(), "absent"))
	team, runs := w.poll()
	require.JSONEq(t, `{"team_id":"","members":[],"tasks":[]}`, string(team))
	require.JSONEq(t, `[]`, string(runs))
	team, runs = w.poll()
	require.Nil(t, team)
	require.Nil(t, runs)
}

func TestTeamWatcherKeepsLastGoodState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTeam(t, dir, agentteam.State{TeamID: "team-1", Members: []agentteam.Member{alice()}})
	w := newTeamWatcher(dir)
	w.poll()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "team.json"), []byte(`{"team_id":`), 0o644))
	team, _ := w.poll()
	require.Nil(t, team, "a broken file publishes nothing")
	cur, _ := w.current()
	require.Equal(t, "team-1", cur.TeamID)
	require.Len(t, cur.Members, 1)
}

func TestTeamStateDropsDeletedTasksAndInbox(t *testing.T) {
	t.Parallel()
	gone := alice()
	gone.ID, gone.Name, gone.Status = "m-2", "carol", agentteam.StatusInactive
	got := teamStateFrom(agentteam.State{
		TeamID:  "team-1",
		Members: []agentteam.Member{alice(), gone},
		Tasks: []agentteam.Task{
			{ID: "task-1", Subject: "Port the parser", Status: agentteam.TaskInProgress, OwnerID: "m-1", SessionID: "s1"},
			{ID: "task-2", Subject: "Gone", Status: agentteam.TaskDeleted, SessionID: "s1"},
		},
		Messages: []agentteam.Message{{ID: "msg-1", From: "lead", To: "alice", Text: "secret plan"}},
	})
	require.Len(t, got.Members, 2, "inactive teammates stay on the roster")
	require.Equal(t, agentteam.StatusInactive, got.Members[1].Status)
	require.Len(t, got.Tasks, 1)
	require.Equal(t, "alice", got.Tasks[0].OwnerName)
	require.Equal(t, []string{}, got.Tasks[0].BlockedBy)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret plan")
}

func TestTeamFramesReachTheTab(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTeam(t, dir, agentteam.State{TeamID: "team-1"})
	writeRuns(t, dir, []agentrun.Run{{ID: "sub-1", Kind: agentrun.KindSubagent, Name: "Map the parser", SessionID: "sub-1", ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: time.Now()}})
	g, _ := startWith(t, Options{AgentTeamsDir: dir, teamPollInterval: 20 * time.Millisecond})
	c := dialStream(t, g, "")
	var snap Snapshot
	require.NoError(t, json.Unmarshal(readFrame(t, c).Payload, &snap))
	require.Equal(t, "team-1", snap.Team.TeamID)
	require.Len(t, snap.AgentRuns, 1)

	writeTeam(t, dir, agentteam.State{TeamID: "team-1", Members: []agentteam.Member{alice()}})
	for range 10 {
		f := readFrame(t, c)
		if f.Type == "team_state" {
			require.Contains(t, string(f.Payload), `"name":"alice"`)
			return
		}
	}
	t.Fatal("no team_state frame after the roster changed")
}

func TestGetSessionRoute(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1", "")
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"id":"s1"`)
	rsp, _ = doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/nope", "")
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
}

func TestTeamRoutesCallTheWorkspace(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/team/members/alice/message", `{"session_id":"s1","text":"rebase first"}`)
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	rsp, _ = doJSON(t, c, http.MethodPost, g.URL()+"/api/team/members/alice/interrupt", `{"session_id":"s1"}`)
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Equal(t, []string{"TeamSendMessage:s1:alice:rebase first", "TeamInterrupt:s1:alice"}, fake.CallLog())
}

func TestTeamRoutesValidate(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	for body, code := range map[string]string{
		`{"text":"hi"}`:                   "missing_session",
		`{"session_id":"s1","text":"  "}`: "empty_message",
		`{"session_id":"s1","text":"` + strings.Repeat("x", agentteam.DefaultMaxMessageBytes+1) + `"}`: "message_too_large",
	} {
		rsp, data := doJSON(t, c, http.MethodPost, g.URL()+"/api/team/members/alice/message", body)
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
		require.Contains(t, string(data), code)
	}
	require.Empty(t, fake.CallLog())
}

func TestTeamRoutesMapErrors(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{agent.ErrAgentTeamsDisabled, http.StatusConflict, "teams_disabled"},
		{&agentteam.Error{Code: agentteam.CodeMemberNotFound, Message: `Team member "bob" not found`}, http.StatusConflict, "team_refused"},
		{errors.New("dial unix /tmp/crush.sock: refused"), http.StatusInternalServerError, "team_failed"},
	}
	for _, tc := range cases {
		fake.SetTeamErr(tc.err)
		rsp, data := doJSON(t, c, http.MethodPost, g.URL()+"/api/team/members/bob/interrupt", `{"session_id":"s1"}`)
		require.Equal(t, tc.status, rsp.StatusCode, tc.code)
		require.Contains(t, string(data), tc.code)
		require.NotContains(t, string(data), "/tmp/crush.sock")
	}
}

// teamStubCoordinator implements only the Team steering methods; the
// embedded nil Coordinator panics on anything else, which flags an
// unexpected call.
type teamStubCoordinator struct {
	agent.Coordinator
	err error
}

func (c *teamStubCoordinator) TeamSendMessage(context.Context, string, string, string) error {
	return c.err
}

func (c *teamStubCoordinator) TeamInterrupt(context.Context, string, string) error {
	return c.err
}

// startClientGateway runs the gateway on a real ClientWorkspace over a real
// server, so the wire round trip a client/server workspace makes is part of
// the test.
func startClientGateway(t *testing.T, coordinator agent.Coordinator) *Gateway {
	t.Helper()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	a.AgentCoordinator = coordinator

	srv := server.NewServer(nil, "tcp", "127.0.0.1:0")
	ws := &backend.Workspace{ID: "ws-1", Path: t.TempDir(), App: a}
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})
	backend.InsertWorkspaceForTest(srv.Backend(), ws)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	u, err := url.Parse(hs.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	cws := workspace.NewClientWorkspace(c, proto.Workspace{ID: ws.ID})
	t.Cleanup(cws.Shutdown)

	g, err := Start(t.Context(), Options{Workspace: cws})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g
}

// TestTeamRoutesMapErrorsOverClientWorkspace pins the error mapping on the
// real client/server path: the server sends a machine code, the SDK turns it
// back into the sentinel or the typed Team error, and the gateway answers
// the same status as it does for an in-process workspace.
func TestTeamRoutesMapErrorsOverClientWorkspace(t *testing.T) {
	t.Parallel()
	const generic = "the workspace could not complete the request"
	cases := []struct {
		name        string
		coordinator agent.Coordinator
		status      int
		code        string
		message     string
	}{
		{
			name:        "teams disabled",
			coordinator: &teamStubCoordinator{err: fmt.Errorf("session s1: %w", agent.ErrAgentTeamsDisabled)},
			status:      http.StatusConflict,
			code:        "teams_disabled",
			message:     "Agent Teams is not enabled",
		},
		{
			name: "team refusal",
			coordinator: &teamStubCoordinator{err: &agentteam.Error{
				Code:    agentteam.CodeMemberNotFound,
				Message: `Team member "bob" not found`,
			}},
			status:  http.StatusConflict,
			code:    "team_refused",
			message: `Team member "bob" not found`,
		},
		{
			// A socket path in the workspace error must not reach the tab.
			name:        "generic failure",
			coordinator: &teamStubCoordinator{err: errors.New("dial unix /tmp/crush.sock: refused")},
			status:      http.StatusInternalServerError,
			code:        "team_failed",
			message:     generic,
		},
		{
			// The gateway has no dedicated code for a workspace whose
			// agent is still starting, so it answers the generic failure
			// instead of the error text naming the missing coordinator.
			name:        "agent not initialized",
			coordinator: nil,
			status:      http.StatusInternalServerError,
			code:        "team_failed",
			message:     generic,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := startClientGateway(t, tc.coordinator)
			c := authedClient(t, g)
			rsp, data := doJSON(t, c, http.MethodPost, g.URL()+"/api/team/members/bob/interrupt", `{"session_id":"s1"}`)
			require.Equal(t, tc.status, rsp.StatusCode, string(data))
			var answer struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			require.NoError(t, json.Unmarshal(data, &answer))
			require.Equal(t, tc.code, answer.Code)
			require.Equal(t, tc.message, answer.Error)
		})
	}
}

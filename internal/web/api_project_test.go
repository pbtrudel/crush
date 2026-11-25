package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestExportRouteWritesTheFile(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.WorkDir = t.TempDir()
	f.AddSession(session.Session{ID: "s1", Title: "Parser"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/export", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "crush-session-s1.md")
	_, err := os.Stat(filepath.Join(f.WorkDir, "crush-session-s1.md"))
	require.NoError(t, err)
}

func TestExportRouteUnknownSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/nope/export", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_session")
}

func TestInitProjectStartsASession(t *testing.T) {
	t.Parallel()
	opened := make(chan string, 1)
	g, f := startWith(t, Options{OnSessionCreated: func(id string) { opened <- id }})
	f.SetInitializePrompt("Initialize this project.")
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/project/init", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	var out struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, out.SessionID, <-opened)
	require.Eventually(t, func() bool {
		return slices.Contains(f.CallLog(), "AgentRun:"+out.SessionID+":Initialize this project.") &&
			slices.Contains(f.CallLog(), "MarkProjectInitialized")
	}, time.Second, 10*time.Millisecond)
}

func TestInitProjectRefusedWhileBusy(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.SetAgentBusy(true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/project/init", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "agent_busy")
}

func TestInitProjectRefusedWhileStarting(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.SetReadyErr(workspace.ErrAgentNotInitialized)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/project/init", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "agent_starting")
}

func TestDockerRoutesAndAvailability(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{DockerMCPAvailable: func() bool { return false }})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/docker-mcp/enable", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "docker_unavailable")
	listRsp, list := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/mcp", "")
	defer listRsp.Body.Close()
	require.Contains(t, string(list), `"docker":{"available":false,"enabled":false}`)
	rsp, _ = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/docker-mcp/disable", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Contains(t, f.CallLog(), "DisableDockerMCP")
}

func TestDockerEnableCallsTheWorkspace(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{DockerMCPAvailable: func() bool { return true }})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/docker-mcp/enable", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Contains(t, f.CallLog(), "EnableDockerMCP")
}

func TestMCPListMarksChannels(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.MCP = map[string]mcp.ClientInfo{"discord": {Name: "discord", State: mcp.StateConnected, ChannelCapable: true}}
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/mcp", "")
	defer rsp.Body.Close()
	require.Contains(t, string(body), `"name":"discord"`)
	require.Contains(t, string(body), `"channel":true`)
}

func TestAgentTeamsRouteWritesAndReloads(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/agent-teams", `{"enabled":true}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Equal(t, []string{"SetConfigField:global:options.agent_teams:true", "ReloadConfig"}, f.CallLog())
}

func TestAgentTeamsRouteBusyReload(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.SetReloadErr(app.ErrConfigReloadBusy)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/agent-teams", `{"enabled":true}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "agent_busy")
	require.Equal(t, []string{"SetConfigField:global:options.agent_teams:true", "ReloadConfig"}, f.CallLog(),
		"the refused reload keeps the write")
}

func TestAgentTeamsRouteWriteFailure(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.SetConfigErr(errors.New("disk /home/secret is full"))
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/agent-teams", `{"enabled":true}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "config_write")
	require.NotContains(t, string(body), "secret", "the store error stays out of the body")
}

func TestAgentTeamsRouteReloadFailure(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.SetReloadErr(errors.New("reload /home/secret/crush.json: parse error"))
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/agent-teams", `{"enabled":true}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "config_write")
	require.NotContains(t, string(body), "secret", "the reload error stays out of the body")
}

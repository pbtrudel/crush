package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// TestSkillsListIncludesConfigDisabled pins the full skill set on the
// wire: a skill the config disabled is listed and flagged, and enabling it
// for this repository clears the effective flag without changing the
// global one.
func TestSkillsListIncludesConfigDisabled(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSkill(skills.CatalogEntry{ID: "a", Name: "go-tests"}, "")
	f.AddDiscoveredSkill(skills.CatalogEntry{ID: "b", Name: "crush-config"}) // Discovered, not active.
	f.Cfg = &config.Config{Options: &config.Options{DisabledSkills: []string{"crush-config"}}}
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/skills", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), `{"name":"crush-config","disabled":true,"disabled_globally":true}`)
	require.Contains(t, string(body), `{"name":"go-tests","disabled":false,"disabled_globally":false}`)

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/skills/crush-config/enable", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body),
		"a config-disabled skill can be re-enabled for this repo")
	require.Contains(t, f.CallLog(), "SkillSetDisabled:crush-config:false")

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/skills", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), `{"name":"crush-config","disabled":false,"disabled_globally":true}`,
		"the repository enable override wins over the config")
}

// TestSkillsListFallsBackWhenTheFullCatalogFails pins the M3 parity rule:
// a workspace that cannot list its full set still answers the active one,
// so the list never empties.
func TestSkillsListFallsBackWhenTheFullCatalogFails(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSkill(skills.CatalogEntry{ID: "a", Name: "go-tests"}, "")
	f.SetAllSkillsErr(errors.New("catalog unavailable: /home/user/.crush/skills"))

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/skills", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), `"name":"go-tests"`)
	require.NotContains(t, string(body), "catalog unavailable")
}

// TestReconnectingOnSkillsRoute drives both lost-link states through the
// skills list: each answers 503 with its own code and the fixed sentence,
// and the route does not fall back to the visible set, which cannot answer
// a lost link either.
func TestReconnectingOnSkillsRoute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"workspace gone", workspace.ErrWorkspaceGone, "workspace_gone"},
		{"server unreachable", fmt.Errorf("list every skill: %w", workspace.ErrServerUnreachable), "server_unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, f := startTest(t)
			f.AddSkill(skills.CatalogEntry{ID: "a", Name: "go-tests"}, "")
			f.SetAllSkillsErr(tc.err)

			rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/skills", "")
			rsp.Body.Close()
			require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
			require.Contains(t, string(body), tc.code)
			require.Contains(t, string(body), "Crush is reconnecting to its server. Try again in a moment.")
			require.Contains(t, f.CallLog(), "ListAllSkills")
			require.NotContains(t, f.CallLog(), "ListSkills", "a lost link answers before the fallback runs")
		})
	}
}

// TestSkillGlobalToggle pins the global skill toggle: the body's flag
// reaches the workspace's config write, and an unknown name or malformed
// body never does.
func TestSkillGlobalToggle(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSkill(skills.CatalogEntry{ID: "a", Name: "go-tests"}, "")
	c := authedClient(t, g)

	for _, tc := range []struct{ body, call string }{
		{`{"disabled":true}`, "SkillSetConfigDisabled:go-tests:true"},
		{`{"disabled":false}`, "SkillSetConfigDisabled:go-tests:false"},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/skills/go-tests/global", tc.body)
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		require.Contains(t, f.CallLog(), tc.call)
	}

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/skills/ghost/global", `{"disabled":true}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_skill")
	require.NotContains(t, f.CallLog(), "SkillSetConfigDisabled:ghost:true")

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/skills/go-tests/global", "{")
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "bad_json")
}

// TestSkillGlobalToggleFailureIsOpaque pins the failure path: a config
// write that fails answers the generic failure, never the store's text,
// which can name the user's config file.
func TestSkillGlobalToggleFailureIsOpaque(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSkill(skills.CatalogEntry{ID: "a", Name: "go-tests"}, "")
	f.SetConfigErr(errors.New("open /home/user/.crush/crushrc: permission denied"))

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/skills/go-tests/global", `{"disabled":true}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "toggle_skill")
	require.NotContains(t, string(body), "crushrc")
}

// TestMCPGlobalToggle pins the global MCP toggle: both actions reach the
// workspace's config write with the matching flag.
func TestMCPGlobalToggle(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.MCP = map[string]mcp.ClientInfo{"github": {Name: "github", State: mcp.StateConnected}}
	c := authedClient(t, g)

	for _, tc := range []struct{ action, call string }{
		{"disable_global", "MCPSetServerConfigDisabled:github:true"},
		{"enable_global", "MCPSetServerConfigDisabled:github:false"},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/mcp/github/"+tc.action, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, tc.action, string(body))
		require.Contains(t, f.CallLog(), tc.call)
	}
}

// TestMCPGlobalToggleFailureIsOpaque pins the failure path: a config
// write that fails answers the generic failure, never the store's text.
func TestMCPGlobalToggleFailureIsOpaque(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.MCP = map[string]mcp.ClientInfo{"github": {Name: "github", State: mcp.StateConnected}}
	f.SetConfigErr(errors.New("open /home/user/.crush/crushrc: permission denied"))

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/mcp/github/disable_global", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "toggle_mcp")
	require.NotContains(t, string(body), "crushrc")
}

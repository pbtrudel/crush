package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestConfigGetAnswersAgentTeams(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	off := false
	fake.Cfg = &config.Config{Options: &config.Options{AgentTeams: &off}}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/config", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"agent_teams":false}`, string(body))
}

// TestConfigWriteRoutesAreGone pins the M0 removal of the display write
// routes: a cached tab gets a 404, not the old key_not_allowed error.
func TestConfigWriteRoutesAreGone(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)
	for _, path := range []string{"/api/config/set", "/api/config/remove"} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+path,
			`{"key":"options.web_display.theme","value":"solid"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, path)
		require.NotContains(t, string(body), "key_not_allowed", path)
	}
}

package web

import "net/http"

// handleConfigGet answers the settings a tab renders at start. The tab's
// display settings lived here until the web UI rebuild; every one of them
// went with the old UI, and only the Agent Teams toggle is left.
func (g *Gateway) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	teams := false
	if cfg := g.ws.Config(); cfg != nil {
		teams = cfg.Options.GetAgentTeams()
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_teams": teams})
}

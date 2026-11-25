package workspace

import "github.com/charmbracelet/crush/internal/config"

// WriteAgentTeams persists the Agent Teams toggle. A workspace config that
// sets the option outranks the global file, so the toggle is written there
// too. The workspace file's contents decide that, not the merged config: a
// busy agent refuses the reload, which would leave the merged value stale.
// The caller reloads the config afterwards.
func WriteAgentTeams(ws Workspace, enabled bool) error {
	scopes := []config.Scope{config.ScopeGlobal}
	if workspaceSetsAgentTeams(ws.Config()) {
		scopes = append(scopes, config.ScopeWorkspace)
	}
	for _, scope := range scopes {
		if err := ws.SetConfigField(scope, "options.agent_teams", enabled); err != nil {
			return err
		}
	}
	return nil
}

// workspaceSetsAgentTeams reports whether the workspace config file sets the
// Agent Teams option, which is what makes a global write lose to it.
func workspaceSetsAgentTeams(cfg *config.Config) bool {
	if cfg == nil || cfg.Options == nil {
		return false
	}
	return config.WorkspaceConfigSets(cfg.Options.DataDirectory, "options.agent_teams")
}

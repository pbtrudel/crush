package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSubAgentPaletteKeepsMCPTools pins that a subagent is offered the MCP
// tools when servers are configured. The read-only task preset used to carry
// an empty MCP allow-list, so a subagent could not call any MCP tool the Lead
// could, and it also lacked the rest of the non-editing tools.
func TestSubAgentPaletteKeepsMCPTools(t *testing.T) {
	env := testEnv(t)

	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	// Pin every input buildTools reads so the test is hermetic against the
	// developer's user config (same reasoning as coderAgent in common_test).
	cfg.Config().Options.Attribution = &config.Attribution{}
	cfg.Config().Options.SkillsPaths = nil
	cfg.Config().Options.ContextPaths = nil
	cfg.Config().Options.GlobalContextPaths = nil
	cfg.Config().LSP = nil
	cfg.Config().Hooks = nil
	cfg.Config().MCP = config.MCPs{"docs": {Command: "docs-server", Lazy: true}}

	c := &coordinator{
		cfg:         cfg,
		permissions: env.permissions,
		history:     env.history,
		filetracker: *env.filetracker,
	}

	built, err := c.buildTools(t.Context(), cfg.Config().Agents[config.AgentTask], roleSubAgent, func() Model { return c.mainModels().Large })
	require.NoError(t, err)

	names := make([]string, 0, len(built))
	for _, tool := range built {
		names = append(names, tool.Info().Name)
	}

	require.Contains(t, names, "list_mcp_resources")
	require.Contains(t, names, "read_mcp_resource")
	require.Contains(t, names, "mcp_servers")
	require.Contains(t, names, "bash")
	require.Contains(t, names, "read")
	require.NotContains(t, names, "edit")
	require.NotContains(t, names, "write")
	require.NotContains(t, names, "agent")
	require.NotContains(t, names, "question")
}

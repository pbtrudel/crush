package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

func TestAgentPresetsExcludeRoles(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()
	require.NotContains(t, availableSubAgentPresets(cfg), "reviewer")
}

// A role agent inherits the coder's tool list, so it is only excluded by
// the "agent" tool check when that tool is enabled. This pins the exclusion
// to the role itself.
func TestAgentPresetsExcludeRolesWithoutAgentTool(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{DisabledTools: []string{AgentToolName}}}
	cfg.SetupAgents()
	require.NotContains(t, availableSubAgentPresets(cfg), "reviewer")
}

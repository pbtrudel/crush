package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveSubAgentPreset(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Agents: map[string]config.Agent{
			config.AgentTask: {ID: "task", AllowedTools: []string{"glob", "grep"}},
			"explorer":       {ID: "explorer", AllowedTools: []string{"view", "grep"}},
			"nested":         {ID: "nested", AllowedTools: []string{"agent"}},
			"off":            {ID: "off", Disabled: true},
		},
	}

	t.Run("defaults to task", func(t *testing.T) {
		t.Parallel()
		preset, err := resolveSubAgentPreset(cfg, "")
		require.NoError(t, err)
		require.Equal(t, config.AgentTask, preset.ID)
	})

	t.Run("selects a configured preset", func(t *testing.T) {
		t.Parallel()
		preset, err := resolveSubAgentPreset(cfg, "explorer")
		require.NoError(t, err)
		require.Equal(t, "explorer", preset.ID)
		require.Equal(t, []string{"view", "grep"}, preset.AllowedTools)
	})

	t.Run("unknown preset lists available names", func(t *testing.T) {
		t.Parallel()
		_, err := resolveSubAgentPreset(cfg, "missing")
		require.ErrorContains(t, err, `unknown agent preset "missing"`)
		require.ErrorContains(t, err, "explorer")
	})

	t.Run("rejects presets that can spawn subagents", func(t *testing.T) {
		t.Parallel()
		_, err := resolveSubAgentPreset(cfg, "nested")
		require.ErrorContains(t, err, "cannot spawn subagents")
	})

	t.Run("rejects disabled presets", func(t *testing.T) {
		t.Parallel()
		_, err := resolveSubAgentPreset(cfg, "off")
		require.ErrorContains(t, err, "disabled")
	})
}

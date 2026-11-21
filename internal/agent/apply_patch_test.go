package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// applyPatchTestCoordinator builds a coordinator whose main model is the
// given one, so the palette can be built without the developer's own config.
func applyPatchTestCoordinator(t *testing.T, enabled bool, provider, model string) *coordinator {
	t.Helper()
	c := lazyTestCoordinator(t, false)
	c.cfg.Config().Options.ApplyPatch = ptr(enabled)
	c.cfg.Config().Models[config.SelectedModelTypeMain] = config.SelectedModel{
		Provider: provider,
		Model:    model,
	}
	return c
}

func applyPatchTestAgent() config.Agent {
	return config.Agent{
		Name:  "test",
		Model: config.SelectedModelTypeMain,
		AllowedTools: []string{
			tools.ApplyPatchToolName,
			tools.EditToolName,
			tools.MultiEditToolName,
			tools.ViewToolName,
			tools.WriteToolName,
		},
	}
}

func buildApplyPatchPalette(t *testing.T, c *coordinator) []string {
	t.Helper()
	built, err := c.buildTools(t.Context(), applyPatchTestAgent(), roleLead, func() Model { return c.mainModels().Large })
	require.NoError(t, err)
	return toolListNames(built)
}

// TestApplyPatchReplacesEditOnlyForGPTModels pins the switch: a GPT model with
// the option on gets apply_patch and neither edit nor multiedit, and every
// other combination keeps the palette it had before the option existed.
func TestApplyPatchReplacesEditOnlyForGPTModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		enabled   bool
		provider  string
		model     string
		wantPatch bool
		wantEdits bool
	}{
		{
			name:      "gpt model with the option on",
			enabled:   true,
			provider:  "openai",
			model:     "gpt-5.1",
			wantPatch: true,
		},
		{
			name:      "gateway gpt model with the option on",
			enabled:   true,
			provider:  "openrouter",
			model:     "openai/o3-mini",
			wantPatch: true,
		},
		{
			name:      "gpt model with the option off",
			enabled:   false,
			provider:  "openai",
			model:     "gpt-5.1",
			wantEdits: true,
		},
		{
			name:      "non-gpt model with the option on",
			enabled:   true,
			provider:  "anthropic",
			model:     "claude-opus-4-5",
			wantEdits: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			names := buildApplyPatchPalette(t, applyPatchTestCoordinator(t, tt.enabled, tt.provider, tt.model))

			if tt.wantPatch {
				require.Contains(t, names, tools.ApplyPatchToolName)
				require.NotContains(t, names, tools.EditToolName)
				require.NotContains(t, names, tools.MultiEditToolName)
				return
			}
			require.NotContains(t, names, tools.ApplyPatchToolName)
			require.Contains(t, names, tools.EditToolName)
			require.Contains(t, names, tools.MultiEditToolName)
		})
	}
}

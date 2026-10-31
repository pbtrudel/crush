package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestUpdateModelsRefreshesAgentModel pins the flow behind the UI's model
// display: after the preferred model changes in config, UpdateModels must
// push the freshly built model into the live agent, so WorkspaceAgentModel
// (fed from Coordinator.Model) reports the selection immediately. The agent
// itself reads models lazily at request time, so without the push the run
// path uses the new model while Model() keeps returning the
// construction-time snapshot and the sidebar looks stale.
func TestUpdateModelsRefreshesAgentModel(t *testing.T) {
	env := testEnv(t)

	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)

	const providerID = "test-openai-compat"
	cfg.Config().Providers.Set(providerID, config.ProviderConfig{
		ID:      providerID,
		Name:    "Test",
		Type:    catwalk.TypeCompletions,
		BaseURL: "http://127.0.0.1:0/v1",
		APIKey:  "test",
		Models: []catwalk.Model{
			{ID: "model-a", DefaultMaxTokens: 4096},
			{ID: "model-b", DefaultMaxTokens: 4096},
		},
	})
	cfg.OverridePreferredModel(config.SelectedModelTypeLarge,
		config.SelectedModel{Provider: providerID, Model: "model-a"})
	cfg.OverridePreferredModel(config.SelectedModelTypeSmall,
		config.SelectedModel{Provider: providerID, Model: "model-a"})
	cfg.SetupAgents()

	coderCfg := cfg.Config().Agents[config.AgentCoder]
	coderCfg.AllowedTools = nil
	cfg.Config().Agents[config.AgentCoder] = coderCfg

	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{
		Config:      cfg,
		Sessions:    env.sessions,
		Messages:    env.messages,
		Permissions: env.permissions,
	})
	require.NoError(t, err)

	require.Equal(t, "model-a", coord.Model().ModelCfg.Model,
		"the coordinator must report the initially configured model")

	cfg.OverridePreferredModel(config.SelectedModelTypeLarge,
		config.SelectedModel{Provider: providerID, Model: "model-b"})
	require.NoError(t, coord.UpdateModels(t.Context()))

	require.Equal(t, "model-b", coord.Model().ModelCfg.Model,
		"UpdateModels must push the new model into the live agent")
}

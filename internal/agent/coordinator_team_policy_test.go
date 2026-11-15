package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// teamToolNames are the tools whose presence makes the Agent Teams policy
// apply. They are spelled out here so the test fails if the surface grows
// without the policy check learning about it.
var teamToolNames = []string{
	"spawn_teammate",
	"send_message",
	"list_agents",
	"wait_agent",
	"interrupt_agent",
	"team_task_create",
	"team_task_list",
	"team_task_get",
	"team_task_update",
}

// newPolicyTestCoordinator builds a real coordinator against a hermetic config
// so the coder's system prompt comes from the production path, the same one a
// user gets. disabledTools mirrors the disabled_tools option.
func newPolicyTestCoordinator(t *testing.T, disabledTools []string) *coordinator {
	t.Helper()

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
		},
	})
	cfg.OverridePreferredModel(config.SelectedModelTypeMain,
		config.SelectedModel{Provider: providerID, Model: "model-a"})
	cfg.OverridePreferredModel(config.SelectedModelTypeSmall,
		config.SelectedModel{Provider: providerID, Model: "model-a"})
	cfg.Config().Options.DisabledTools = disabledTools
	cfg.SetupAgents()

	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{
		Config:      cfg,
		Sessions:    env.sessions,
		Messages:    env.messages,
		Permissions: env.permissions,
		Interactive: true,
	})
	require.NoError(t, err)

	// The constructor builds the prompt and tools in the background; the
	// concrete type is needed for that group and for the agent map.
	impl, ok := coord.(*coordinator)
	require.True(t, ok, "NewCoordinator must return the concrete coordinator")
	require.NoError(t, impl.readyWg.Wait())

	return impl
}

// The coder prompt is the one that renders the policy, and its palette is the
// one a user can cut down with disabled_tools. The gate only works if the
// coder prompt is told which tools the coder actually has, which is what this
// pins: without that argument the policy is rendered for everyone.
func TestCoderPromptRendersTeamPolicyOnlyWithTeamTools(t *testing.T) {
	t.Run("default palette", func(t *testing.T) {
		coord := newPolicyTestCoordinator(t, nil)

		coderCfg := coord.cfg.Config().Agents[config.AgentCoder]
		require.Contains(t, coderCfg.AllowedTools, "spawn_teammate",
			"the default coder palette must hold the team tools for this test to mean anything")

		require.Contains(t, coord.agents[config.AgentCoder].SystemPrompt(), "## Agent Teams")
	})

	t.Run("team tools disabled", func(t *testing.T) {
		coord := newPolicyTestCoordinator(t, teamToolNames)

		coderCfg := coord.cfg.Config().Agents[config.AgentCoder]
		for _, name := range teamToolNames {
			require.NotContains(t, coderCfg.AllowedTools, name,
				"disabled_tools must remove the tool from the palette")
		}

		require.NotContains(t, coord.agents[config.AgentCoder].SystemPrompt(), "## Agent Teams",
			"an agent with no team tools must not be told to use them")
	})
}

// Dropping the policy must not take the rest of the prompt with it.
func TestCoderPromptWithDisabledTeamToolsKeepsTheRest(t *testing.T) {
	coord := newPolicyTestCoordinator(t, teamToolNames)

	prompt := coord.agents[config.AgentCoder].SystemPrompt()
	require.Contains(t, prompt, "<critical_rules>")
	require.Contains(t, prompt, "<tool_usage>")
	require.NotContains(t, prompt, "Use it by default: the Lead coordinates and delegates")
}

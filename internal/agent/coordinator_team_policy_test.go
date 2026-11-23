package agent

import (
	"slices"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agent/tools"
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

// policyTestSetup is how a policy test wants its coordinator built.
type policyTestSetup struct {
	// interactive mirrors the TUI run mode, which is what decides whether
	// delegation blocks or detaches.
	interactive bool
	// disabledTools mirrors the disabled_tools option.
	disabledTools []string
	// agentTeams forces the Agent Teams setting on or off. nil leaves the
	// default, which is on.
	agentTeams *bool
	// profile is the prompt_profile the config carries. Empty leaves it
	// unset, which follows the measured default for the model's family.
	profile string
	// mainModel is the model ID the provider serves and the config selects.
	// Empty selects model-a.
	mainModel string
}

// policyTestDeepSeek is the model ID a test selects when it wants a model the
// measured lean default covers.
const policyTestDeepSeek = "deepseek-v4.1-flash"

// newPolicyTestCoordinator builds a real coordinator against a hermetic config
// so the coder's system prompt comes from the production path, the same one a
// user gets.
func newPolicyTestCoordinator(t *testing.T, setup policyTestSetup) *coordinator {
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
			{ID: policyTestDeepSeek, DefaultMaxTokens: 4096},
		},
	})
	modelID := setup.mainModel
	if modelID == "" {
		modelID = "model-a"
	}
	cfg.OverridePreferredModel(config.SelectedModelTypeMain,
		config.SelectedModel{Provider: providerID, Model: modelID})
	cfg.OverridePreferredModel(config.SelectedModelTypeSmall,
		config.SelectedModel{Provider: providerID, Model: modelID})
	cfg.Config().Options.DisabledTools = setup.disabledTools
	cfg.Config().Options.AgentTeams = setup.agentTeams
	cfg.Config().Options.PromptProfile = setup.profile
	cfg.SetupAgents()

	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{
		Config:      cfg,
		Sessions:    env.sessions,
		Messages:    env.messages,
		Permissions: env.permissions,
		Interactive: setup.interactive,
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
		coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})

		coderCfg := coord.cfg.Config().Agents[config.AgentCoder]
		require.Contains(t, coderCfg.AllowedTools, "spawn_teammate",
			"the default coder palette must hold the team tools for this test to mean anything")

		require.Contains(t, coord.agents[config.AgentCoder].SystemPrompt(), "## Agent Teams")
	})

	t.Run("team tools disabled", func(t *testing.T) {
		coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true, disabledTools: teamToolNames})

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
	// The prompt asserted here is the full one, so the profile is explicit.
	coord := newPolicyTestCoordinator(t, policyTestSetup{
		interactive:   true,
		disabledTools: teamToolNames,
		profile:       config.PromptProfileFull,
	})

	prompt := coord.agents[config.AgentCoder].SystemPrompt()
	require.Contains(t, prompt, "<critical_rules>")
	require.Contains(t, prompt, "<tool_usage>")
	require.NotContains(t, prompt, "Use it by default: the Lead coordinates and delegates")
}

// delegationToolNames are the per-agent delegation tools the coder palette
// drops when Agent Teams is off.
var delegationToolNames = []string{
	AgentDispatchToolName,
	AgentSendToolName,
	AgentStatusToolName,
	AgentStopToolName,
}

// coderToolNames returns the tools the coder agent currently holds.
func coderToolNames(t *testing.T, coord *coordinator) []string {
	t.Helper()
	agent, ok := coord.agents[config.AgentCoder].(*sessionAgent)
	require.True(t, ok, "the coder agent is a session agent")
	held := agent.tools.Copy()
	names := make([]string, 0, len(held))
	for _, tool := range held {
		names = append(names, tool.Info().Name)
	}
	return names
}

// With Agent Teams off, nothing that hands work to another agent is attached.
// agentic_fetch stays: it runs a read-only helper inside its own call.
func TestAgentTeamsOffRemovesDelegationTools(t *testing.T) {
	on := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})
	palette := coderToolNames(t, on)
	for _, name := range append(slices.Clone(delegationToolNames), teamToolNames...) {
		require.Contains(t, palette, name, "the default palette must hold the delegation surface")
	}

	off := false
	disabled := newPolicyTestCoordinator(t, policyTestSetup{interactive: true, agentTeams: &off})
	palette = coderToolNames(t, disabled)
	for _, name := range append(slices.Clone(delegationToolNames), teamToolNames...) {
		require.NotContains(t, palette, name, "turned-off agents must not be offered")
	}
	require.Contains(t, palette, tools.AgenticFetchToolName, "agentic_fetch is not a delegation tool")
	require.Contains(t, palette, "bash")
}

// A headless run keeps the blocking agent tool while Agent Teams is on, and
// loses it with the rest of the delegation surface when it is off.
func TestAgentTeamsOffRemovesTheLegacyAgentToolInRunMode(t *testing.T) {
	on := newPolicyTestCoordinator(t, policyTestSetup{})
	require.Contains(t, coderToolNames(t, on), AgentToolName)

	off := false
	disabled := newPolicyTestCoordinator(t, policyTestSetup{agentTeams: &off})
	require.NotContains(t, coderToolNames(t, disabled), AgentToolName)
}

// Turning Agent Teams off rebuilds the palette of the live agents, so the
// change takes effect without a restart.
func TestAgentTeamsOffRebuildsTheLivePalette(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})
	require.Contains(t, coderToolNames(t, coord), AgentDispatchToolName)

	// Stand in for the toggle's config write: the reload reads the setting
	// from the published config and rebuilds the palette from it.
	off := false
	coord.cfg.Config().Options.AgentTeams = &off
	require.NoError(t, coord.RefreshModels(t.Context()))

	palette := coderToolNames(t, coord)
	require.NotContains(t, palette, AgentDispatchToolName)
	require.NotContains(t, palette, SpawnTeammateToolName)
	require.Contains(t, palette, tools.AgenticFetchToolName)

	// Turning it back on restores the whole surface.
	on := true
	coord.cfg.Config().Options.AgentTeams = &on
	require.NoError(t, coord.RefreshModels(t.Context()))
	palette = coderToolNames(t, coord)
	require.Contains(t, palette, AgentDispatchToolName)
	require.Contains(t, palette, SpawnTeammateToolName)
}

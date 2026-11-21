package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// promptProfileStore returns a test store whose only settings are the prompt
// profile and Agent Teams (and any context paths the test names), so a
// rendered prompt depends on nothing else.
func promptProfileStore(profile string, contextPaths ...string) *config.ConfigStore {
	teamsOn := true
	return config.NewTestStore(&config.Config{Options: &config.Options{
		PromptProfile: profile,
		AgentTeams:    &teamsOn,
		ContextPaths:  contextPaths,
	}})
}

// coderPromptForProfile renders the coder prompt through the production
// template path for one profile and model. The working directory is a
// repository fixture whose random path is hidden, so the text is stable.
func coderPromptForProfile(t *testing.T, profile, provider, model string) string {
	t.Helper()
	dir := promptWorkDir(t, true)
	return coderPromptTextFor(t, promptProfileStore(profile), dir, provider, model)
}

// coderPromptTextFor renders the coder prompt for one store and directory.
func coderPromptTextFor(t *testing.T, store *config.ConfigStore, dir, provider, model string) string {
	t.Helper()
	p, err := coderPrompt(
		prompt.WithWorkingDir(dir),
		prompt.WithPlatform("linux"),
		prompt.WithToolNames([]string{"spawn_teammate", "agent_dispatch", "bash", "view", "grep", "edit"}),
	)
	require.NoError(t, err)
	built, err := p.Build(t.Context(), provider, model, store)
	require.NoError(t, err)
	return strings.ReplaceAll(built, dir, "/work")
}

// The profile decides which template set an agent renders: full is today's
// prompt, lean is the short base plus the add-on for the model's family.
func TestPromptProfileSelectsTheTemplateSet(t *testing.T) {
	t.Parallel()

	full := coderPromptForProfile(t, config.PromptProfileFull, "anthropic", "claude-sonnet-4-5")
	lean := coderPromptForProfile(t, config.PromptProfileLean, "anthropic", "claude-sonnet-4-5")
	require.NotEqual(t, full, lean)

	// The unset profile is the full prompt, byte for byte: a new option must
	// not change what every existing session is given.
	require.Equal(t, full, coderPromptForProfile(t, "", "anthropic", "claude-sonnet-4-5"))

	// Sections that only the full prompt carries stay out of the lean one,
	// because the tool descriptions carry the same guidance.
	for _, fullOnly := range []string{"<tool_usage>", "<editing_files>", "<proactiveness>"} {
		require.Contains(t, full, fullOnly)
		require.NotContains(t, lean, fullOnly)
	}
}

// A family is chosen from the model ID first and the provider second, so a
// gateway serving several families classifies by the model it serves.
func TestPromptFamilySelection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{name: "anthropic provider", provider: "anthropic", model: "claude-sonnet-4-5", want: familyAnthropic},
		{name: "claude on a gateway", provider: "openrouter", model: "anthropic/claude-opus-4-1", want: familyAnthropic},
		{name: "bedrock claude", provider: "bedrock", model: "us.anthropic.claude-haiku-4-5", want: familyAnthropic},
		{name: "claude agent", provider: "claude-agent", model: "claude-sonnet-4-5", want: familyAnthropic},
		{name: "openai provider", provider: "openai", model: "gpt-5-codex", want: familyGPT},
		{name: "gpt on a gateway", provider: "vercel", model: "openai/gpt-5", want: familyGPT},
		{name: "codex model", provider: "openai", model: "codex-mini-latest", want: familyGPT},
		{name: "azure deployment name", provider: "azure", model: "my-gpt-deployment", want: familyGPT},
		{name: "reasoning model id", provider: "openai", model: "o3-mini", want: familyGPT},
		{name: "gemini", provider: "google", model: "gemini-2.5-pro", want: familyDefault},
		{name: "unknown provider", provider: "mock", model: "mock-model", want: familyDefault},
		{name: "empty", want: familyDefault},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, promptFamily(test.provider, test.model))
		})
	}
}

// Each family renders the shared base plus its own add-on, so the golden
// text shows exactly what a model of that family is given.
func TestLeanCoderPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		family   string
		provider string
		model    string
	}{
		{family: familyAnthropic, provider: "anthropic", model: "claude-sonnet-4-5"},
		{family: familyGPT, provider: "openai", model: "gpt-5"},
		{family: familyDefault, provider: "google", model: "gemini-2.5-pro"},
	} {
		t.Run(test.family, func(t *testing.T) {
			t.Parallel()
			want, err := os.ReadFile(filepath.Join("testdata", "lean_prompt_"+test.family+".golden"))
			require.NoError(t, err)

			got := coderPromptForProfile(t, config.PromptProfileLean, test.provider, test.model)
			require.Contains(t, got, "Is directory a git repo: yes", "the fixture must be the repository the golden names")
			require.Equal(t, string(want), got)
		})
	}
}

// Everything load-bearing survives the lean profile: the rules that keep the
// agent safe and correct, the environment, the skills, and the context files
// the user and the project supply.
func TestLeanPromptKeepsLoadBearingSections(t *testing.T) {
	t.Parallel()

	dir := promptWorkDir(t, true)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("PROJECT-INSTRUCTION"), 0o644))

	store := promptProfileStore(config.PromptProfileLean, filepath.Join(dir, "AGENTS.md"))
	got := coderPromptTextFor(t, store, dir, "anthropic", "claude-sonnet-4-5")

	for _, text := range []string{
		"<critical_rules>",
		"READ THE RELEVANT CONTEXT BEFORE EDITING",
		"NEVER COMMIT",
		"NEVER PUSH TO REMOTE",
		"SECURITY FIRST",
		"LOAD MATCHING SKILLS",
		"<communication_style>",
		"<workflow>",
		"<environment>",
		"Is directory a git repo: yes",
		"<project_context>",
		"PROJECT-INSTRUCTION",
		"<skills_usage>",
		"## Agent Teams",
	} {
		require.Contains(t, got, text)
	}
}

// The lean profile exists to cost less on every request, so it has to be
// substantially smaller than the full one for every family.
func TestLeanPromptIsSmallerThanFull(t *testing.T) {
	t.Parallel()

	full := coderPromptForProfile(t, config.PromptProfileFull, "anthropic", "claude-sonnet-4-5")
	t.Logf("full prompt: %d bytes", len(full))

	for _, test := range []struct {
		provider string
		model    string
	}{
		{provider: "anthropic", model: "claude-sonnet-4-5"},
		{provider: "openai", model: "gpt-5"},
		{provider: "google", model: "gemini-2.5-pro"},
	} {
		lean := coderPromptForProfile(t, config.PromptProfileLean, test.provider, test.model)
		t.Logf("lean prompt (%s/%s): %d bytes", test.provider, test.model, len(lean))
		require.Less(t, len(lean), len(full)/2, "the lean prompt for %s has to be less than half the full one", test.model)
	}
}

// A config reload applies a profile change to the live agent, the way the
// Agent Teams setting does: the option is written at runtime, so a session
// must not need a restart to pick it up.
func TestPromptProfileReloadRerendersTheLivePrompt(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})
	before := coord.agents[config.AgentCoder].SystemPrompt()
	require.Contains(t, before, "<proactiveness>", "the live prompt is the full one")
	require.NotContains(t, before, "<family_notes>")

	coord.cfg.Config().Options.PromptProfile = config.PromptProfileLean
	require.NoError(t, coord.RefreshModels(t.Context()))

	after := coord.agents[config.AgentCoder].SystemPrompt()
	require.NotContains(t, after, "<proactiveness>")
	require.Contains(t, after, "<family_notes>")

	coord.cfg.Config().Options.PromptProfile = config.PromptProfileFull
	require.NoError(t, coord.RefreshModels(t.Context()))
	require.Equal(t, before, coord.agents[config.AgentCoder].SystemPrompt())
}

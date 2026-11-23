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
// prompt, lean is the short base plus the add-on for the model's family. An
// unset profile follows the measurement, so DeepSeek gets the lean prompt and
// every other model keeps full until its own batch lands.
func TestPromptProfileSelectsTheTemplateSet(t *testing.T) {
	t.Parallel()

	full := coderPromptForProfile(t, config.PromptProfileFull, "anthropic", "claude-sonnet-4-5")
	lean := coderPromptForProfile(t, config.PromptProfileLean, "anthropic", "claude-sonnet-4-5")
	require.NotEqual(t, full, lean)

	// A model the lean profile was not measured on keeps the full prompt when
	// the profile is unset, whichever add-on family it belongs to.
	require.Equal(t, full, coderPromptForProfile(t, "", "anthropic", "claude-sonnet-4-5"))
	require.Equal(t, full, coderPromptForProfile(t, "", "openai", "gpt-5"))
	require.Equal(t, full, coderPromptForProfile(t, "", "google", "gemini-2.5-pro"))
	require.Equal(t, full, coderPromptForProfile(t, "", "moonshot", "kimi-k3.1"))

	// DeepSeek is where the lean profile was measured, so an unset profile
	// renders the lean prompt for it.
	unsetDeepSeek := coderPromptForProfile(t, "", "deepseek", "deepseek-v4.1-flash")
	require.Equal(t,
		coderPromptForProfile(t, config.PromptProfileLean, "deepseek", "deepseek-v4.1-flash"),
		unsetDeepSeek)

	// Sections that only the full prompt carries stay out of the lean one,
	// because the tool descriptions carry the same guidance.
	for _, fullOnly := range []string{"<tool_usage>", "<editing_files>", "<proactiveness>"} {
		require.Contains(t, full, fullOnly)
		require.NotContains(t, lean, fullOnly)
		require.NotContains(t, unsetDeepSeek, fullOnly)
	}
}

// The effective profile is the one a coder session really renders. Only
// DeepSeek, the vendor the lean profile was measured on, takes the lean
// default; an explicit setting wins everywhere, and a name the config does not
// know keeps the full prompt.
func TestEffectivePromptProfile(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		setting  string
		provider string
		model    string
		want     string
	}{
		{name: "unset deepseek model", provider: "deepseek", model: "deepseek-v4.1-flash", want: config.PromptProfileLean},
		{name: "unset deepseek model on another provider", provider: "neuralwatt", model: "deepseek-v4.1-flash", want: config.PromptProfileLean},
		{name: "unset deepseek model through a gateway", provider: "commandcode", model: "deepseek/deepseek-v4.1-flash", want: config.PromptProfileLean},
		{name: "unset deepseek distill", provider: "openrouter", model: "deepseek-r1-distill-qwen-32b", want: config.PromptProfileLean},
		{name: "unset unclassified model", provider: "google", model: "gemini-2.5-pro", want: config.PromptProfileFull},
		{name: "unset unknown model", want: config.PromptProfileFull},
		{name: "unset bedrock deepseek id fails safe", provider: "bedrock", model: "us.deepseek.r1-v1:0", want: config.PromptProfileFull},
		{name: "unset kimi k3.1", provider: "moonshot", model: "kimi-k3.1", want: config.PromptProfileFull},
		{name: "unset kimi on a gateway", provider: "commandcode", model: "kimi/kimi-k3.1", want: config.PromptProfileFull},
		{name: "unset glm", provider: "zai", model: "glm-4.6", want: config.PromptProfileFull},
		{name: "unset qwen", provider: "alibaba", model: "qwen3-max", want: config.PromptProfileFull},
		{name: "unset mimo", provider: "xiaomi", model: "mimo-v2-pro", want: config.PromptProfileFull},
		{name: "unset anthropic provider", provider: "anthropic", model: "claude-sonnet-4-5", want: config.PromptProfileFull},
		{name: "unset anthropic model on a gateway", provider: "openrouter", model: "anthropic/claude-opus-4-1", want: config.PromptProfileFull},
		{name: "unset gpt provider", provider: "openai", model: "gpt-5", want: config.PromptProfileFull},
		{name: "unset gpt model on a gateway", provider: "vercel", model: "openai/gpt-5", want: config.PromptProfileFull},
		{name: "explicit full on deepseek", setting: config.PromptProfileFull, provider: "deepseek", model: "deepseek-v4.1-flash", want: config.PromptProfileFull},
		{name: "explicit full on anthropic", setting: config.PromptProfileFull, provider: "anthropic", model: "claude-sonnet-4-5", want: config.PromptProfileFull},
		{name: "explicit lean on deepseek", setting: config.PromptProfileLean, provider: "deepseek", model: "deepseek-v4.1-flash", want: config.PromptProfileLean},
		{name: "explicit lean on anthropic", setting: config.PromptProfileLean, provider: "anthropic", model: "claude-sonnet-4-5", want: config.PromptProfileLean},
		{name: "explicit lean on gpt", setting: config.PromptProfileLean, provider: "openai", model: "gpt-5", want: config.PromptProfileLean},
		{name: "explicit lean on kimi", setting: config.PromptProfileLean, provider: "moonshot", model: "kimi-k3.1", want: config.PromptProfileLean},
		{name: "unknown name on deepseek", setting: "tiny", provider: "deepseek", model: "deepseek-v4.1-flash", want: config.PromptProfileFull},
		{name: "unknown name on anthropic", setting: "tiny", provider: "anthropic", model: "claude-sonnet-4-5", want: config.PromptProfileFull},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, effectivePromptProfile(test.setting, test.provider, test.model))
		})
	}
}

// A coder prompt the user replaced through options.prompts renders as written
// for every profile, so the measured default cannot reach it.
func TestCustomCoderPromptIgnoresTheProfileDefault(t *testing.T) {
	t.Parallel()

	custom := filepath.Join(t.TempDir(), "coder.md")
	require.NoError(t, os.WriteFile(custom, []byte("CUSTOM-CODER-PROMPT\n"), 0o644))
	teamsOn := true
	store := config.NewTestStore(&config.Config{Options: &config.Options{
		AgentTeams: &teamsOn,
		Prompts:    config.PromptPaths{"coder": custom},
	}})

	p, err := loadPrompt(store, "coder", prompt.WithWorkingDir(promptWorkDir(t, false)), prompt.WithPlatform("linux"))
	require.NoError(t, err)
	for _, test := range []struct{ provider, model string }{
		{provider: "deepseek", model: "deepseek-v4.1-flash"},
		{provider: "anthropic", model: "claude-sonnet-4-5"},
	} {
		got, err := p.Build(t.Context(), test.provider, test.model, store)
		require.NoError(t, err)
		require.Equal(t, "CUSTOM-CODER-PROMPT\n", got)
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
// must not need a restart to pick it up. The fixture runs a DeepSeek model,
// so an unset profile is the lean prompt.
func TestPromptProfileReloadRerendersTheLivePrompt(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{
		interactive: true,
		mainModel:   policyTestDeepSeek,
	})
	unset := coord.agents[config.AgentCoder].SystemPrompt()
	require.NotContains(t, unset, "<proactiveness>", "the live prompt follows the measured default")
	require.Contains(t, unset, "<family_notes>")

	coord.cfg.Config().Options.PromptProfile = config.PromptProfileFull
	require.NoError(t, coord.RefreshModels(t.Context()))
	afterFull := coord.agents[config.AgentCoder].SystemPrompt()
	require.Contains(t, afterFull, "<proactiveness>", "explicit full beats the measured default")
	require.NotContains(t, afterFull, "<family_notes>")

	coord.cfg.Config().Options.PromptProfile = ""
	require.NoError(t, coord.RefreshModels(t.Context()))
	require.Equal(t, unset, coord.agents[config.AgentCoder].SystemPrompt())
}

// The reload input has to name the profile the next render really uses, so a
// setting that changes the effective default re-renders the prompt, and one
// that does not leaves the cached prefix alone.
func TestPromptInputFollowsTheEffectiveProfile(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{
		interactive: true,
		mainModel:   policyTestDeepSeek,
	})
	agentCfg := coord.cfg.Config().Agents[config.AgentCoder]

	require.Equal(t, config.PromptProfileLean, coord.promptInputFor(agentCfg).profile,
		"an unset profile on DeepSeek is the lean prompt")

	coord.cfg.Config().Options.PromptProfile = config.PromptProfileFull
	require.Equal(t, config.PromptProfileFull, coord.promptInputFor(agentCfg).profile)

	coord.cfg.Config().Options.PromptProfile = ""
	coord.models.Set(coordinatorModels{large: Model{Model: &profileModel{provider: "anthropic", id: "claude-sonnet-4-5"}}})
	require.Equal(t, config.PromptProfileFull, coord.promptInputFor(agentCfg).profile,
		"an unset profile on a model that was not measured stays full")
}

// profileModel is a language model whose provider and model ID a test names.
type profileModel struct {
	fakeLanguageModel
	provider string
	id       string
}

func (m *profileModel) Provider() string { return m.provider }
func (m *profileModel) Model() string    { return m.id }

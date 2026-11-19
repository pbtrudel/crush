package agent

import (
	"slices"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// leadPrompt builds the Lead's system prompt through the production path,
// with the palette its coder agent is configured with.
func leadPrompt(t *testing.T) (string, []string) {
	t.Helper()

	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	palette := cfg.Config().Agents[config.AgentCoder].AllowedTools
	p, err := coderPrompt(
		prompt.WithPlatform("linux"),
		prompt.WithWorkingDir(env.workingDir),
		prompt.WithToolNames(palette),
	)
	require.NoError(t, err)

	built, err := p.Build(t.Context(), "test-provider", "test-model", cfg)
	require.NoError(t, err)
	return built, palette
}

// A prompt that names a tool the agent does not hold sends the model after
// something it cannot call. The prompt is static text, so every tool name it
// mentions has to be one the Lead's default palette carries.
func TestLeadPromptMentionsOnlyToolsInItsPalette(t *testing.T) {
	t.Parallel()

	built, palette := leadPrompt(t)

	// The full tool list, plus the names the prompt used to advertise: the
	// subagent's web_fetch, the coder's own fetch, and a find_references
	// tool that never existed.
	candidates := append(slices.Clone(palette), "web_fetch", "web_search", "find_references")
	for _, name := range candidates {
		if slices.Contains(palette, name) {
			continue
		}
		require.NotContains(t, built, name, "the prompt names %q, which the Lead cannot call", name)
	}
}

// These statements were untrue of the code: edits are whitespace-tolerant,
// no tool is called find_references, the coder's fetch tool is not web_fetch,
// whole-file reads are discouraged elsewhere in the same prompt, and pushing
// requires the user's ask. FS_STALE_VERSION is an error string no code
// returns, so the Team policy cannot cite it.
func TestPromptStatementsMatchTheCode(t *testing.T) {
	t.Parallel()

	built, _ := leadPrompt(t)

	for _, text := range []string{"Read entire file before editing", "extremely literal", "FS_STALE_VERSION"} {
		require.NotContains(t, built, text)
		require.NotContains(t, agentteam.Policy, text)
	}

	require.Contains(t, built, "If the user asked for it, commit and push the branch from the worktree")
	require.Contains(t, agentteam.Policy, "If an edit or write is refused because the file changed since you read it")
}

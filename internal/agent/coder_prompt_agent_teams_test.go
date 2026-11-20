package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// The delegation lines in the coder prompt and anything else Agent Teams
// decides are spelled out here, so a test fails if the wording moves and the
// gate does not follow it.
const (
	waitOnSubagentsLine = "- Waiting on subagents started with `agent_dispatch`"
	subagentReportLine  = "a `<subagent-report>`, a teammate message or a todo reminder is a continuation"
	backgroundOnlyLine  = "- A `<background_job_notice>` or a todo reminder is a continuation"
	delegateTodoLine    = "- When you delegate a todo to a teammate or subagent"
)

// coderPromptText renders the coder prompt from the production template. The
// fixed working directory and platform keep the rendered text stable, and the
// tool list holds a team tool so the policy gate itself is not what drops the
// policy.
func coderPromptText(t *testing.T, teamsOn bool) string {
	t.Helper()
	store := config.NewTestStore(&config.Config{Options: &config.Options{AgentTeams: &teamsOn}})
	p, err := coderPrompt(
		prompt.WithWorkingDir("/work"),
		prompt.WithPlatform("linux"),
		prompt.WithToolNames([]string{"spawn_teammate", "agent_dispatch", "agentic_fetch", "bash", "view"}),
	)
	require.NoError(t, err)
	got, err := p.Build(t.Context(), "mock", "mock-model", store)
	require.NoError(t, err)
	return got
}

// Turning Agent Teams on must leave the coder prompt byte for byte as it was
// before the setting gated anything.
func TestCoderPromptWithAgentTeamsOnMatchesGolden(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("testdata", "coder_prompt_agent_teams_on.golden"))
	require.NoError(t, err)
	require.Equal(t, string(want), coderPromptText(t, true))
}

// With Agent Teams off the prompt carries no coordination section and no
// delegation rules, and keeps the rules that still apply.
func TestCoderPromptDropsDelegationWhenAgentTeamsAreOff(t *testing.T) {
	t.Parallel()

	off := coderPromptText(t, false)
	require.NotContains(t, off, "## Agent Teams")
	require.NotContains(t, off, waitOnSubagentsLine)
	require.NotContains(t, off, subagentReportLine)
	require.NotContains(t, off, delegateTodoLine)
	require.Contains(t, off, backgroundOnlyLine, "background jobs still report back")
	require.Contains(t, off, "<critical_rules>")
	require.Contains(t, off, "<tool_usage>")

	on := coderPromptText(t, true)
	for _, line := range []string{waitOnSubagentsLine, subagentReportLine, delegateTodoLine} {
		require.Contains(t, on, line)
	}
	require.NotContains(t, on, backgroundOnlyLine)
}

// A config reload re-renders the prompt of the live agents, so the setting
// takes effect in the session that changed it, both ways.
func TestAgentTeamsToggleReRendersTheLivePrompt(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})
	onPrompt := coord.agents[config.AgentCoder].SystemPrompt()
	require.Contains(t, onPrompt, "## Agent Teams")
	require.Contains(t, onPrompt, waitOnSubagentsLine)

	// Stand in for the toggle's config write: the reload reads the setting
	// from the published config.
	off := false
	coord.cfg.Config().Options.AgentTeams = &off
	require.NoError(t, coord.RefreshModels(t.Context()))

	prompt := coord.agents[config.AgentCoder].SystemPrompt()
	require.NotContains(t, prompt, "## Agent Teams")
	require.NotContains(t, prompt, waitOnSubagentsLine)
	require.NotContains(t, prompt, delegateTodoLine)
	require.Contains(t, prompt, "<critical_rules>")

	on := true
	coord.cfg.Config().Options.AgentTeams = &on
	require.NoError(t, coord.RefreshModels(t.Context()))
	require.Contains(t, coord.agents[config.AgentCoder].SystemPrompt(), "## Agent Teams")
}

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

// The delegation lines in the coder prompt and anything else Agent Teams
// decides are spelled out here, so a test fails if the wording moves and the
// gate does not follow it.
const (
	waitOnSubagentsLine = "- Waiting on subagents started with `agent_dispatch`"
	subagentReportLine  = "a `<subagent-report>`, a teammate message or a todo reminder is a continuation"
	backgroundOnlyLine  = "- A `<background_job_notice>` or a todo reminder is a continuation"
	delegateTodoLine    = "- When you delegate a todo to a teammate or subagent"
)

// promptWorkDir returns a working directory of the test's own. repo marks it
// as a git checkout with the .git entry the prompt looks for, so the rendered
// repository flag never depends on where the test itself runs.
func promptWorkDir(t *testing.T, repo bool) string {
	t.Helper()
	dir := t.TempDir()
	if repo {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: nowhere\n"), 0o644))
	}
	return dir
}

// coderPromptTextIn renders the coder prompt from the production template for
// one working directory. The fixed platform keeps the rendered text stable,
// and the tool list holds a team tool so the policy gate itself is not what
// drops the policy.
func coderPromptTextIn(t *testing.T, workDir string, teamsOn bool) string {
	t.Helper()
	// The golden pins the full coder prompt, so the profile is explicit: an
	// unset profile renders lean for the default family the mock model is in.
	store := config.NewTestStore(&config.Config{Options: &config.Options{
		AgentTeams:    &teamsOn,
		PromptProfile: config.PromptProfileFull,
	}})
	p, err := coderPrompt(
		prompt.WithWorkingDir(workDir),
		prompt.WithPlatform("linux"),
		prompt.WithToolNames([]string{"spawn_teammate", "agent_dispatch", "agentic_fetch", "bash", "view"}),
	)
	require.NoError(t, err)
	got, err := p.Build(t.Context(), "mock", "mock-model", store)
	require.NoError(t, err)
	return got
}

// coderPromptText renders for the repository fixture, with its random path
// hidden so the golden text is stable.
func coderPromptText(t *testing.T, teamsOn bool) string {
	t.Helper()
	dir := promptWorkDir(t, true)
	return strings.ReplaceAll(coderPromptTextIn(t, dir, teamsOn), dir, "/work")
}

// The repository flag describes the directory the prompt names, not the one
// the test happens to run from.
func TestCoderPromptGitFlagFollowsTheWorkingDir(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		repo bool
		want string
	}{
		{name: "repository", repo: true, want: "Is directory a git repo: yes"},
		{name: "plain directory", want: "Is directory a git repo: no"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := coderPromptTextIn(t, promptWorkDir(t, test.repo), true)
			require.Contains(t, got, test.want)
		})
	}
}

// Turning Agent Teams on must leave the coder prompt byte for byte as it was
// before the setting gated anything.
func TestCoderPromptWithAgentTeamsOnMatchesGolden(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("testdata", "coder_prompt_agent_teams_on.golden"))
	require.NoError(t, err)
	got := coderPromptText(t, true)
	require.Contains(t, got, "Is directory a git repo: yes", "the fixture must be the repository the golden names")
	require.Equal(t, string(want), got)
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
	// The lines below are the full prompt's: an unset profile follows the
	// measured default for this fixture's model.
	coord := newPolicyTestCoordinator(t, policyTestSetup{
		interactive: true,
		profile:     config.PromptProfileFull,
	})
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

// A reload that changes neither the Agent Teams setting nor the tool palette
// must leave the prompt alone: re-rendering it would also fold in things the
// tool block does not depend on and rewrite the cached prompt prefix.
func TestRefreshModelsKeepsThePromptWhenNothingRelevantChanged(t *testing.T) {
	coord := newPolicyTestCoordinator(t, policyTestSetup{interactive: true})
	before := coord.agents[config.AgentCoder].SystemPrompt()
	require.NotContains(t, before, "LATE_INSTRUCTION")

	// A new context file is exactly what a re-render would pick up.
	require.NoError(t, os.WriteFile(
		filepath.Join(coord.cfg.WorkingDir(), "CRUSH.md"),
		[]byte("LATE_INSTRUCTION"),
		0o644,
	))
	require.NoError(t, coord.RefreshModels(t.Context()))

	require.Equal(t, before, coord.agents[config.AgentCoder].SystemPrompt())
}

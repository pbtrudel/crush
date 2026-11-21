package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
)

// The two context chains a subagent prompt has to tell apart. The project
// chain carries the rules the subagent works under; the global chain carries
// the user's procedures for a top-level agent, which a subagent does not run.
const (
	subagentProjectMarker = "project-rule-marker"
	subagentGlobalMarker  = "global-rule-marker"
	subagentParentLine    = "Rules about commits, pull requests and delegation in the project context below address your parent agent, not you."
)

// isolatedGlobalConfig keeps the test off the developer's global config and
// returns the directory that holds the global context chain, which sits beside
// the Crush config directory.
func isolatedGlobalConfig(t *testing.T) string {
	t.Helper()
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv("CRUSH_GLOBAL_CONFIG", configDir)
	t.Setenv("CRUSH_GLOBAL_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CRUSH_DISABLE_PROVIDER_AUTO_UPDATE", "1")
	return filepath.Dir(configDir)
}

// writeGlobalContext gives the global chain a file with the global marker, so
// a prompt that renders it is caught.
func writeGlobalContext(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"AGENTS.md", filepath.Join("config", "CRUSH.md")} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(subagentGlobalMarker), 0o644))
	}
}

// buildTaskPromptText renders the subagent prompt through the production
// template for one working directory.
func buildTaskPromptText(t *testing.T, workingDir string, canWrite bool, configure func(*config.ConfigStore)) string {
	t.Helper()
	return buildPromptText(t, workingDir, taskPrompt, configure, prompt.WithCanWrite(canWrite))
}

// buildPromptText renders one production prompt over a config loaded for
// workingDir. The fixed platform keeps the rendered text stable.
func buildPromptText(t *testing.T, workingDir string, constructor func(...prompt.Option) (*prompt.Prompt, error), configure func(*config.ConfigStore), opts ...prompt.Option) string {
	t.Helper()
	store, err := config.Init(t.Context(), workingDir, "", false)
	require.NoError(t, err)
	if configure != nil {
		configure(store)
	}
	opts = append([]prompt.Option{prompt.WithWorkingDir(workingDir), prompt.WithPlatform("linux")}, opts...)
	p, err := constructor(opts...)
	require.NoError(t, err)
	built, err := p.Build(t.Context(), "mock", "mock-model", store)
	require.NoError(t, err)
	return built
}

// contextFilePattern matches one file the prompt rendered: the template puts
// the content between a newline and the closing tag.
var contextFilePattern = regexp.MustCompile(`(?s)<file path="[^"]*">\n(.*?)\n</file>`)

// newGitRepo returns a repository root, so context paths resolve against the
// project root and not against the working directory alone.
func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git init: %s", out)
	return dir
}

// projectContextSection returns what the prompt placed between the project
// context markers.
func projectContextSection(t *testing.T, built string) string {
	t.Helper()
	_, after, ok := strings.Cut(built, "<project_context>")
	require.True(t, ok, "the prompt carries a project context section")
	section, _, ok := strings.Cut(after, "</project_context>")
	require.True(t, ok, "the project context section is closed")
	return section
}

// renderedContextFiles returns the content of every file a project context
// section rendered, which is what the byte budget bounds.
func renderedContextFiles(t *testing.T, section string) []string {
	t.Helper()
	matches := contextFilePattern.FindAllStringSubmatch(section, -1)
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		files = append(files, match[1])
	}
	return files
}

// A subagent works under the project's rules. It does not run the user's
// commit, pull-request or delegation procedures, so the global chain stays
// out and one line says whose rules the shared ones are.
func TestTaskPromptCarriesProjectRulesOnly(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(subagentProjectMarker), 0o644))
	globalDir := isolatedGlobalConfig(t)
	writeGlobalContext(t, globalDir)

	built := buildTaskPromptText(t, workingDir, false, nil)

	require.Contains(t, built, subagentProjectMarker,
		"a subagent must get the project's instructions, as the coder does")
	require.NotContains(t, built, subagentGlobalMarker,
		"the global chain holds commit, pull-request and delegation procedures for a top-level agent")
	require.Contains(t, built, subagentParentLine,
		"the shared rules must say whose they are")
	require.Less(t, strings.Index(built, subagentParentLine), strings.Index(built, "<rules>"),
		"the line about the parent agent belongs before the rules")
}

// The coder prompt is the one the top-level agent and every teammate run
// under. What a subagent drops is not general: the coder keeps both chains,
// the project's rules and the user's procedures.
func TestCoderPromptKeepsBothContextChains(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(subagentProjectMarker), 0o644))
	globalDir := isolatedGlobalConfig(t)
	writeGlobalContext(t, globalDir)

	built := buildPromptText(t, workingDir, coderPrompt, nil)

	require.Contains(t, built, subagentProjectMarker,
		"the coder works under the project's rules")
	require.Contains(t, built, subagentGlobalMarker,
		"the coder runs the user's commit, pull-request and delegation procedures")
}

// The project chain is read from the directory the prompt names, so the
// rendered rules and the rendered working directory describe the same place.
func TestTaskPromptReadsRulesFromItsWorkingDir(t *testing.T) {
	sessionDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "AGENTS.md"), []byte("session-marker"), 0o644))
	taskDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(taskDir, "AGENTS.md"), []byte(subagentProjectMarker), 0o644))
	isolatedGlobalConfig(t)

	store, err := config.Init(t.Context(), sessionDir, "", false)
	require.NoError(t, err)
	p, err := taskPrompt(prompt.WithWorkingDir(taskDir), prompt.WithPlatform("linux"))
	require.NoError(t, err)
	built, err := p.Build(t.Context(), "mock", "mock-model", store)
	require.NoError(t, err)

	require.Contains(t, built, subagentProjectMarker)
	require.NotContains(t, built, "session-marker")
}

// The project chain reaches the subagent under the same byte budget the coder
// prompt uses, so a large AGENTS.md cannot crowd out the task itself.
func TestTaskPromptBudgetsProjectRules(t *testing.T) {
	workingDir := t.TempDir()
	body := strings.Repeat("a rule the subagent may need\n", 200)
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(body), 0o644))
	isolatedGlobalConfig(t)

	const budget = 400
	built := buildTaskPromptText(t, workingDir, false, func(store *config.ConfigStore) {
		store.Config().Options.ContextMaxBytes = new(budget)
	})

	require.Contains(t, built, "Context budget 400 bytes:")
	require.Contains(t, built, "context truncated to fit the context budget",
		"the oversized project file is truncated to the budget")
	require.NotContains(t, built, body, "the whole file does not fit the budget")

	files := renderedContextFiles(t, projectContextSection(t, built))
	require.Len(t, files, 1)
	var rendered int
	for _, file := range files {
		rendered += len(file)
	}
	require.LessOrEqual(t, rendered, budget,
		"the project rules the subagent sees must fit the budget")
}

// Broader files are dropped before the most specific one is cut, so a large
// file at the repository root cannot cost a subagent the rules its own
// package keeps.
func TestTaskPromptDropsTheBroaderProjectFileFirst(t *testing.T) {
	root := newGitRepo(t)
	broader := "broader-rule-marker\n" + strings.Repeat("b", 280)
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(broader), 0o644))
	pkg := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	specific := "specific-rule-marker\n" + strings.Repeat("s", 280)
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "AGENTS.md"), []byte(specific), 0o644))
	isolatedGlobalConfig(t)

	const budget = 400
	built := buildTaskPromptText(t, pkg, false, func(store *config.ConfigStore) {
		store.Config().Options.ContextMaxBytes = new(budget)
	})

	section := projectContextSection(t, built)
	files := renderedContextFiles(t, section)
	require.Len(t, files, 1, "the broader file goes first")
	require.Contains(t, files[0], "specific-rule-marker")
	require.Contains(t, files[0], strings.Repeat("s", 280),
		"the package's own rules survive whole")
	require.NotContains(t, section, "broader-rule-marker")
	require.Contains(t, section, "omitted",
		"the budget notice names the file that was dropped")
	require.Contains(t, section, "Context budget 400 bytes:")
}

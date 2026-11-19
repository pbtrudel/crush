package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

// probeTemplate renders the two things the project root contributes to a
// prompt: the context files and the git state.
const probeTemplate = `{{range .ContextFiles}}file:{{.Content}}
{{end}}git: {{if .IsGitRepo}}yes{{else}}no{{end}}`

// initIsolatedConfig loads the config for dir without reading the
// developer's global config or probing provider catalogs.
func initIsolatedConfig(t *testing.T, dir string) *config.ConfigStore {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CRUSH_GLOBAL_CONFIG", filepath.Join(home, "config"))
	t.Setenv("CRUSH_GLOBAL_DATA", filepath.Join(home, "data"))
	t.Setenv("CRUSH_DISABLE_PROVIDER_AUTO_UPDATE", "1")
	t.Setenv("CRUSH_CLAUDE_PATH", filepath.Join(home, "no-claude"))

	store, err := config.Init(t.Context(), dir, "", false)
	require.NoError(t, err)
	return store
}

// buildProbe renders probeTemplate for the config loaded at store.
func buildProbe(t *testing.T, store *config.ConfigStore) string {
	t.Helper()
	p, err := NewPrompt("probe", probeTemplate)
	require.NoError(t, err)
	out, err := p.Build(t.Context(), "test-provider", "test-model", store)
	require.NoError(t, err)
	return out
}

// A session started in a subdirectory must still read the instructions kept
// at the repository root. The root is where config loading already stops.
func TestPromptInSubdirectoryReadsRootInstructions(t *testing.T) {
	root := newRepo(t, "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root-marker"), 0o644))
	pkg := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(pkg, 0o755))

	out := buildProbe(t, initIsolatedConfig(t, pkg))
	require.Contains(t, out, "root-marker",
		"a prompt built in a subdirectory must carry the repository root's instructions")
	require.Contains(t, out, "git: yes")
}

// Outside a repository the working directory is the boundary, so an
// AGENTS.md above it belongs to some other project and stays out.
func TestPromptOutsideRepositoryIgnoresParentInstructions(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent-marker"), 0o644))
	dir := filepath.Join(parent, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	out := buildProbe(t, initIsolatedConfig(t, dir))
	require.NotContains(t, out, "parent-marker",
		"nothing above the working directory is read outside a repository")
	require.Contains(t, out, "git: no")
}

// A directory with a .git entry that git cannot read is still a repository
// as far as the agent is concerned: the model was told so before the root
// search landed, and a broken git must not change that. The root was not
// found, though, so nothing above the directory is read.
func TestPromptKeepsADirectoryWithABrokenGit(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent-marker"), 0o644))
	dir := filepath.Join(parent, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	out := buildProbe(t, initIsolatedConfig(t, dir))
	require.Contains(t, out, "git: yes",
		"a .git entry marks the directory as a repository even when git cannot read it")
	require.NotContains(t, out, "parent-marker",
		"the upward context search still needs a root git found")
}

// Two context files whose names differ only in case are two files on a
// case-sensitive filesystem, so the dedupe key must not fold them together.
func TestProcessContextPathKeepsCaseDifferingFiles(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes")
	require.NoError(t, os.MkdirAll(notes, 0o755))
	upper := filepath.Join(notes, "AGENTS.md")
	lower := filepath.Join(notes, "agents.md")
	require.NoError(t, os.WriteFile(upper, []byte("upper-marker"), 0o644))
	require.NoError(t, os.WriteFile(lower, []byte("lower-marker"), 0o644))
	upperInfo, err := os.Stat(upper)
	require.NoError(t, err)
	lowerInfo, err := os.Stat(lower)
	require.NoError(t, err)
	if os.SameFile(upperInfo, lowerInfo) {
		t.Skip("this filesystem is case-insensitive")
	}

	contexts := processContextPath("notes", initIsolatedConfig(t, dir))
	require.Len(t, contexts, 2, "both files are real and both must be read")
}

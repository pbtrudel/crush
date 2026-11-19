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

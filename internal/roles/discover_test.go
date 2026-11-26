package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testWriteTools = []string{"apply_patch", "edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol", "worktree"}

func writeRole(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644))
}

func TestDiscoverBuiltins(t *testing.T) {
	t.Parallel()
	found, diags := Discover(nil, nil)
	require.Empty(t, diags)
	require.ElementsMatch(t, []string{"architect", "researcher", "reviewer", "implementer", "verifier"}, keys(found))
	headings := map[string][]string{
		"architect":   {"### Goal", "### Task breakdown", "### Open questions"},
		"researcher":  {"### Question", "### Answer", "### Evidence"},
		"reviewer":    {"### Verdict", "### Findings", "### Questions"},
		"implementer": {"### Files changed", "### Commands run", "### Acceptance criteria"},
		"verifier":    {"### Criteria", "### Verdict", "### Notes"},
	}
	for name, role := range found {
		require.Equal(t, SourceBuiltin, role.Source, name)
		require.False(t, role.PinsModel(), "built-in %s must not pin a model", name)
		require.NotContains(t, role.Body, "\u2014", "built-in %s contains an em dash", name)
		for _, h := range headings[name] {
			require.Contains(t, role.Body, h, "built-in %s", name)
		}
	}
	for _, name := range []string{"architect", "researcher", "reviewer", "verifier"} {
		require.True(t, found[name].ReadOnly(testWriteTools), "%s must be read-only", name)
	}
	require.False(t, found["implementer"].ReadOnly(testWriteTools))
}

func TestDiscoverMissingDirs(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	found, diags := Discover([]Dir{{Path: missing, Source: SourceGlobal}}, nil)
	require.Empty(t, diags)
	require.Len(t, found, 5)
}

func TestDiscoverPriorityAndOverlay(t *testing.T) {
	t.Parallel()
	global := filepath.Join(t.TempDir(), "global")
	root := filepath.Join(t.TempDir(), "root")
	wd := filepath.Join(t.TempDir(), "wd")

	writeRole(t, global, "reviewer", "---\nname: reviewer\nmodel: strong\nprovider: p\n---\n")
	writeRole(t, global, "docs-writer", "---\nname: docs-writer\ndescription: Writes docs.\n---\nWrite docs.\n")
	writeRole(t, root, "docs-writer", "---\nname: docs-writer\ndescription: Root docs.\n---\nRoot body.\n")
	writeRole(t, wd, "docs-writer", "---\nname: docs-writer\nreasoning: high\n---\n")

	found, diags := Discover([]Dir{
		{Path: global, Source: SourceGlobal},
		{Path: root, Source: SourceProject},
		{Path: wd, Source: SourceProject},
	}, nil)
	require.Empty(t, diags)

	reviewer := found["reviewer"]
	require.Equal(t, "strong", reviewer.Model)
	require.Equal(t, SourceGlobal, reviewer.Source)
	require.Contains(t, reviewer.Body, "You are the reviewer.", "the overlay keeps the built-in body")

	docs := found["docs-writer"]
	require.Equal(t, "Root body.", docs.Body, "the git-root replacement wins over the global file")
	require.Equal(t, "Root docs.", docs.Description)
	require.Equal(t, "high", docs.Reasoning, "the working-dir overlay stacks on the root file")
	require.Equal(t, filepath.Join(wd, "docs-writer.md"), docs.Path)
}

func TestDiscoverDiagnostics(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRole(t, dir, "orphan", "---\nname: orphan\nmodel: m\n---\n")
	writeRole(t, dir, "broken", "no header")
	writeRole(t, dir, "picky", "---\nname: picky\ndescription: d\ntools: view, not_a_tool, mcp_x_y, lsp_*\n---\nBody.\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644))

	found, diags := Discover([]Dir{{Path: dir, Source: SourceGlobal}}, []string{"view", "lsp_definition"})
	require.NotContains(t, found, "orphan")
	require.NotContains(t, found, "broken")
	require.Equal(t, []string{"view", "mcp_x_y", "lsp_*"}, found["picky"].Tools,
		"unknown exact names are dropped; MCP names and patterns are kept")

	var text []string
	for _, d := range diags {
		text = append(text, d.String())
	}
	joined := strings.Join(text, "\n")
	require.Contains(t, joined, "orphan.md: an overlay")
	require.Contains(t, joined, "broken.md: no YAML frontmatter")
	require.Contains(t, joined, `picky.md: unknown tool "not_a_tool" dropped`)
	require.NotContains(t, joined, "notes.txt")
}

func TestDiscoverBuiltinToolsAreKnown(t *testing.T) {
	t.Parallel()
	// Mirrors config.AllToolNames(); config imports roles, so the list is
	// repeated here and Task 3 pins the real list from the config side.
	known := []string{
		"view", "read", "ls", "glob", "grep", "bash", "sourcegraph", "todos",
		"fetch", "download", "list_mcp_resources", "read_mcp_resource",
		"job_output", "job_kill", "lsp_diagnostics", "lsp_definition",
		"lsp_references", "lsp_symbols", "lsp_call_hierarchy",
	}
	_, diags := Discover(nil, known)
	require.Empty(t, diags)
}

func keys(m map[string]Role) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

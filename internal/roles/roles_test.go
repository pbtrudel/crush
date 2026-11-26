package roles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllows(t *testing.T) {
	t.Parallel()
	all := Role{Name: "implementer"}
	require.True(t, all.Allows("edit"), "nil Tools allows every tool")

	r := Role{Name: "reviewer", Tools: []string{"view", "mcp_*"}}
	require.True(t, r.Allows("view"))
	require.True(t, r.Allows("mcp_tavily_tavily_search"))
	require.False(t, r.Allows("edit"))
	require.False(t, r.Allows("viewer"), "exact names do not match prefixes")
}

func TestReadOnly(t *testing.T) {
	t.Parallel()
	write := []string{"apply_patch", "edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol", "worktree"}
	require.False(t, Role{}.ReadOnly(write), "nil Tools is a write role")
	require.True(t, Role{Tools: []string{"view", "bash", "lsp_definition"}}.ReadOnly(write))
	require.False(t, Role{Tools: []string{"view", "lsp_*"}}.ReadOnly(write), "lsp_* matches lsp_rename")
}

func TestBlockIsStableAndSorted(t *testing.T) {
	t.Parallel()
	r := Role{Name: "reviewer", Body: "Review things.", Tools: []string{"view", "bash"}}
	want := "## Your role: reviewer\n\nReview things.\n\nAllowed tools: bash, view\nTeam coordination tools are always available.\n\n---\n\n"
	require.Equal(t, want, Block(r))
	require.Equal(t, Block(r), Block(r))
	require.Equal(t, []string{"view", "bash"}, r.Tools, "Block must not sort the caller's slice")

	all := Role{Name: "implementer", Body: "Build."}
	require.Contains(t, Block(all), "Allowed tools: all tools\n")
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	list := []Role{
		{Name: "verifier", Description: "Checks.", Source: SourceBuiltin, Tools: []string{"view", "bash"}},
		{Name: "reviewer", Description: "Reviews.", Source: SourceProject, Model: "m"},
		{Name: "implementer", Description: "Builds.", Source: SourceBuiltin},
		{Name: "architect", Description: "Designs.", Source: SourceGlobal, Model: "m", Tools: []string{"view"}},
	}
	want := "- architect (global, read-only): Designs.\n" +
		"- implementer (builtin, can write): Builds.\n" +
		"- reviewer (project, can write, pins model): Reviews.\n" +
		"- verifier (builtin, read-only): Checks.\n"
	require.Equal(t, want, Catalog(list, testWriteTools))
}

// TestCatalogReportsWidenedOverlay pins that the access marker follows the
// resolved tool list: an overlay that only widens a read-only built-in is
// shown as a write role, whatever the built-in description says.
func TestCatalogReportsWidenedOverlay(t *testing.T) {
	t.Parallel()
	widened := Role{Name: "reviewer", Description: "Reviews.", Source: SourceProject, Tools: []string{"view", "edit"}}
	narrowed := Role{Name: "reviewer", Description: "Reviews.", Source: SourceProject, Tools: []string{"view"}}
	require.Contains(t, Catalog([]Role{widened}, testWriteTools), "- reviewer (project, can write): ")
	require.Contains(t, Catalog([]Role{narrowed}, testWriteTools), "- reviewer (project, read-only): ")
}

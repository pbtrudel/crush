package tools

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
)

// TestReadMCPResourceReadsBuiltinSkill pins that a crush:// skills URI serves
// the embedded skill text, the same file view reads, instead of asking an MCP
// server that does not own it.
func TestReadMCPResourceReadsBuiltinSkill(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()})
	tool := NewReadMCPResourceTool(cfg, &mockPermissionService{})

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{
		ID:    "r1",
		Name:  ReadMCPResourceToolName,
		Input: `{"mcp_name":"crush","uri":"crush://skills/git-workflow/SKILL.md"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "git-workflow")
}

// TestReadMCPResourceRejectsTraversal pins that a crush:// path can never
// reach outside the embedded skill tree.
func TestReadMCPResourceRejectsTraversal(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()})
	tool := NewReadMCPResourceTool(cfg, &mockPermissionService{})

	for _, uri := range []string{
		"crush://skills/builtin/../x",
		"crush://skills/../go.mod",
		"crush://skills/git-workflow/../../README.md",
		"crush://skills/",
		"crush://skills",
	} {
		resp, err := tool.Run(t.Context(), fantasy.ToolCall{
			ID:    "r1",
			Name:  ReadMCPResourceToolName,
			Input: `{"mcp_name":"crush","uri":"` + uri + `"}`,
		})
		require.NoError(t, err)
		require.True(t, resp.IsError, "%s: %s", uri, resp.Content)
	}
}

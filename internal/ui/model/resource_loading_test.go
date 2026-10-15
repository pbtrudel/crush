package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// stubConfigWorkspace satisfies the workspace methods the resource
// sections touch (Config for the MCP list) without a real workspace.
type stubConfigWorkspace struct {
	workspace.Workspace
}

func (stubConfigWorkspace) Config() *config.Config { return &config.Config{} }

func TestResourceSectionsShowLoadingUntilFirstRefresh(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com = common.DefaultCommon(stubConfigWorkspace{})

	// Before the first refresh lands the sections show a loading hint
	// instead of a misleading "None".
	require.Contains(t, u.lspInfo(30, 5, false), "Loading")
	require.Contains(t, u.mcpInfo(30, 5, false), "Loading")

	u.applyLSPStates(lspStatesMsg{
		states:      map[string]workspace.LSPClientInfo{},
		diagnostics: map[string]lsp.DiagnosticCounts{},
	})
	u.applyMCPStates(mcpStateChangedMsg{states: map[string]mcp.ClientInfo{}})

	// Once the first refresh lands, an empty result reads "None".
	require.Contains(t, u.lspInfo(30, 5, false), "None")
	require.Contains(t, u.mcpInfo(30, 5, false), "None")
}

func TestModelInfoShowsPlaceholderUntilAgentReady(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	require.Contains(t, u.modelInfo(60), "…")
}

package tools

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHasLazyMCPServers(t *testing.T) {
	store, err := config.Init(t.TempDir(), "", false)
	require.NoError(t, err)

	store.Config().MCP = config.MCPs{
		"eager": {Type: config.MCPStdio, Command: "true"},
	}
	require.False(t, HasLazyMCPServers(store), "no lazy server means no activation tool")

	store.Config().MCP = config.MCPs{
		"eager": {Type: config.MCPStdio, Command: "true"},
		"lazy":  {Type: config.MCPStdio, Command: "true", Lazy: true},
	}
	require.True(t, HasLazyMCPServers(store))
}

func TestListInactiveMCPServers(t *testing.T) {
	store, err := config.Init(t.TempDir(), "", false)
	require.NoError(t, err)
	store.Config().MCP = config.MCPs{
		"lazy-one": {Type: config.MCPStdio, Command: "true", Lazy: true},
		"off":      {Type: config.MCPStdio, Command: "true", Disabled: true},
	}

	out := listInactiveMCPServers(store)
	require.Contains(t, out, "lazy-one")
	require.NotContains(t, out, "off", "disabled servers are not activatable")
}

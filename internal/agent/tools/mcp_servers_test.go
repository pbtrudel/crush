package tools

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHasLazyMCPServers(t *testing.T) {
	store, err := config.Init(t.Context(), t.TempDir(), "", false)
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
	store, err := config.Init(t.Context(), t.TempDir(), "", false)
	require.NoError(t, err)
	store.Config().MCP = config.MCPs{
		"lazy-one": {Type: config.MCPStdio, Command: "true", Lazy: true},
		"off":      {Type: config.MCPStdio, Command: "true", Disabled: true},
	}

	out := listInactiveMCPServers(store)
	require.Contains(t, out, "lazy-one")
	require.NotContains(t, out, "off", "disabled servers are not activatable")
}

// TestActivationReplyMentionsToolSearchWhenLazy covers the one way activation
// can mislead a lazy session: the reply lists the server's tools, but their
// schemas are deferred, so a model that tries to call one straight away gets
// "tool not found". The reply has to say so.
func TestActivationReplyMentionsToolSearchWhenLazy(t *testing.T) {
	reply := "MCP server \"github\" is connected. Tools available from your next step:\n- create_issue: open an issue"

	require.Equal(t, reply, withLazyActivationHint(reply, false),
		"a full-palette reply is untouched")

	lazyReply := withLazyActivationHint(reply, true)
	require.Contains(t, lazyReply, reply, "the activation reply itself is kept")
	require.Contains(t, lazyReply, ToolSearchToolName,
		"a lazy session must be told the schemas are not in its palette yet")
}

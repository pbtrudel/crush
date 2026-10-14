package tools

import (
	"context"
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
)

// MCPServersToolName is the tool that lists and starts lazily-configured MCP
// servers.
const MCPServersToolName = "mcp_servers"

//go:embed mcp_servers.md
var mcpServersDescription string

type MCPServersParams struct {
	Action string `json:"action" description:"One of: list (show servers that are not running yet), activate (start a server so its tools become available)"`
	Server string `json:"server,omitempty" description:"Server name to activate (required for action=activate)"`
}

// HasLazyMCPServers reports whether any configured MCP server is marked lazy,
// i.e. whether the activation tool is worth registering at all.
func HasLazyMCPServers(cfg *config.ConfigStore) bool {
	for _, server := range cfg.Config().MCP {
		if server.Lazy {
			return true
		}
	}
	return false
}

// NewMCPServersTool creates the tool that lists and activates MCP servers
// which are configured but not started (lazy loading). Its description is
// static on purpose: a per-turn list of servers would change the tool block
// and invalidate the provider's prompt cache.
func NewMCPServersTool(cfg *config.ConfigStore) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		MCPServersToolName,
		mcpServersDescription,
		func(ctx context.Context, params MCPServersParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			switch strings.ToLower(strings.TrimSpace(params.Action)) {
			case "", "list":
				return fantasy.NewTextResponse(listInactiveMCPServers(cfg)), nil
			case "activate":
				return activateMCPServer(ctx, cfg, strings.TrimSpace(params.Server))
			default:
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("unknown action %q: use \"list\" or \"activate\"", params.Action),
				), nil
			}
		},
	)
}

func listInactiveMCPServers(cfg *config.ConfigStore) string {
	states := mcp.GetStates()
	var lines []string
	for name, server := range cfg.Config().MCP {
		if server.Disabled {
			continue
		}
		if info, ok := states[name]; ok && info.State != mcp.StateLazy {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", name, server.Type))
	}
	if len(lines) == 0 {
		return "No MCP servers are waiting to be started."
	}
	slices.Sort(lines)
	return "MCP servers that are not running yet:\n" + strings.Join(lines, "\n") +
		"\n\nActivate one with action \"activate\" and its name."
}

func activateMCPServer(ctx context.Context, cfg *config.ConfigStore, name string) (fantasy.ToolResponse, error) {
	if name == "" {
		return fantasy.NewTextErrorResponse("server parameter is required for action=activate"), nil
	}
	server, ok := cfg.Config().MCP[name]
	if !ok {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("no MCP server named %q is configured", name)), nil
	}
	if info, ok := mcp.GetStates()[name]; ok {
		switch info.State {
		case mcp.StateConnected:
			return fantasy.NewTextResponse(describeMCPServerTools(name)), nil
		case mcp.StateDisabled:
			return fantasy.NewTextErrorResponse(
				fmt.Sprintf("MCP server %q is disabled; enable it with the /mcp command", name),
			), nil
		}
	}
	if server.Disabled {
		return fantasy.NewTextErrorResponse(
			fmt.Sprintf("MCP server %q is disabled; enable it with the /mcp command", name),
		), nil
	}

	if err := mcp.InitializeSingleForced(ctx, name, cfg); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("could not start MCP server %q: %s", name, err)), nil
	}

	// Re-read the state: the server may have landed in needs-auth or error
	// instead of connecting.
	info, _ := mcp.GetStates()[name]
	switch info.State {
	case mcp.StateNeedsAuth:
		return fantasy.NewTextResponse(fmt.Sprintf(
			"MCP server %q needs authentication. Ask the user to sign in from the /mcp dialog, then activate it again.",
			name,
		)), nil
	case mcp.StateConnected:
		return fantasy.NewTextResponse(describeMCPServerTools(name)), nil
	default:
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"MCP server %q did not start (state: %s)", name, info.State.String(),
		)), nil
	}
}

// describeMCPServerTools renders the tools a connected server exposes, plus
// any instructions it sent during initialization, so the model knows what it
// can call from its next step.
func describeMCPServerTools(name string) string {
	tools, _ := mcp.GetTools(name)
	var sb strings.Builder
	if len(tools) == 0 {
		fmt.Fprintf(&sb, "MCP server %q is connected but exposes no tools.", name)
	} else {
		fmt.Fprintf(&sb, "MCP server %q is connected. Tools available from your next step:", name)
		for _, tool := range tools {
			description := firstLine(tool.Description)
			if description != "" {
				fmt.Fprintf(&sb, "\n- %s: %s", tool.Name, description)
			} else {
				fmt.Fprintf(&sb, "\n- %s", tool.Name)
			}
		}
	}
	if info, ok := mcp.GetStates()[name]; ok && info.Client != nil {
		if instructions := info.Client.InitializeResult().Instructions; instructions != "" {
			fmt.Fprintf(&sb, "\n\nServer instructions:\n%s", instructions)
		}
	}
	return sb.String()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	const maxLen = 160
	if len(line) > maxLen {
		return line[:maxLen] + "…"
	}
	return line
}

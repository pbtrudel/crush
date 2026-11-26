package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

type noInput struct{}

func roleRecordingTool(name string, ran map[string]bool) fantasy.AgentTool {
	return fantasy.NewAgentTool(name, "test tool "+name,
		func(_ context.Context, _ noInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ran[name] = true
			return fantasy.NewTextResponse("ok"), nil
		})
}

func runTool(t *testing.T, tool fantasy.AgentTool) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: "{}"})
	require.NoError(t, err)
	return resp
}

func TestRoleGateRefusesDisallowedTool(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	role := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	gated := gateToolsForRole(role, []fantasy.AgentTool{
		roleRecordingTool("view", ran),
		roleRecordingTool("edit", ran),
		roleRecordingTool(SendMessageToolName, ran),
		roleRecordingTool(TeamTaskUpdateToolName, ran),
	})
	for _, tool := range gated {
		runTool(t, tool)
	}
	require.True(t, ran["view"])
	require.False(t, ran["edit"], "a disallowed tool must not run")
	require.True(t, ran[SendMessageToolName], "team coordination tools always pass")
	require.True(t, ran[TeamTaskUpdateToolName], "team coordination tools always pass")

	resp := runTool(t, gated[1])
	require.True(t, resp.IsError)
	require.Equal(t, `tool "edit" is not available to role "reviewer". Allowed tools: view`, resp.Content)
}

func TestRoleGateMatchesLateMCPTool(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	role := &roles.Role{Name: "researcher", Tools: []string{"view", "mcp_*"}}
	gated := gateToolsForRole(role, []fantasy.AgentTool{roleRecordingTool("mcp_tavily_tavily_search", ran)})
	runTool(t, gated[0])
	require.True(t, ran["mcp_tavily_tavily_search"])
}

func TestRoleGateKeepsToolInfo(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	inner := roleRecordingTool("edit", ran)
	gated := gateToolsForRole(&roles.Role{Name: "r", Tools: []string{"view"}}, []fantasy.AgentTool{inner})
	require.Equal(t, inner.Info(), gated[0].Info(), "the gate must not change what the provider sees")
	again := gateToolsForRole(&roles.Role{Name: "r", Tools: []string{"view"}}, gated)
	_, double := again[0].(*roleGatedTool).inner.(*roleGatedTool)
	require.False(t, double, "re-gating must not stack wrappers")
	require.Same(t, inner, gateToolsForRole(nil, []fantasy.AgentTool{inner})[0], "no role, no wrapper")
}

func TestSetToolsAppliesRoleGate(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	agent := NewSessionAgent(SessionAgentOptions{Role: &roles.Role{Name: "reviewer", Tools: []string{"view"}}}).(*sessionAgent)
	agent.SetTools([]fantasy.AgentTool{roleRecordingTool("edit", ran)})
	runTool(t, agent.tools.Copy()[0])
	require.False(t, ran["edit"])
}

func TestWithRoleBlock(t *testing.T) {
	t.Parallel()
	role := &roles.Role{Name: "reviewer", Body: "Review."}
	require.Equal(t, "state", withRoleBlock(nil, "state"))
	require.Equal(t, roles.Block(*role)+"state", withRoleBlock(role, "state"))
	require.Equal(t, roles.Block(*role), withRoleBlock(role, ""), "a role alone still produces a state message")
}

func TestResolveRole(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()

	role, refusal := resolveRole(cfg, "reviewer")
	require.Empty(t, refusal)
	require.Equal(t, "reviewer", role.Name)

	_, refusal = resolveRole(cfg, "task")
	require.Contains(t, refusal, `unknown role "task"`)
	_, refusal = resolveRole(cfg, "nope")
	require.Contains(t, refusal, "available: architect, implementer, researcher, reviewer, verifier")
}

func TestRoleCatalog(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()
	catalog := roleCatalog(cfg)
	require.Contains(t, catalog, "\n\nRoles (pass one as `role`):\n- architect (builtin, read-only): ")
	require.Contains(t, catalog, "\n- implementer (builtin, can write): ")
	require.Equal(t, catalog, roleCatalog(cfg), "the catalog must be byte-stable")
}

// roleMCPTool is an MCP-backed tool the gate tests can identify by server.
type roleMCPTool struct {
	name   string
	server string
}

func (t *roleMCPTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: t.name} }

func (*roleMCPTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.NewTextResponse("ok"), nil
}

func (*roleMCPTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (*roleMCPTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (t *roleMCPTool) MCP() string { return t.server }

func TestRoleGateReportsMCPIdentity(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	gated := gateToolsForRole(&roles.Role{Name: "researcher", Tools: []string{"mcp_*", "view"}}, []fantasy.AgentTool{
		&roleMCPTool{name: "mcp_signal_send", server: "signal"},
		roleRecordingTool("view", ran),
	})
	mcpTool, ok := gated[0].(interface{ MCP() string })
	require.True(t, ok, "the gate must expose the wrapped tool's MCP server")
	require.Equal(t, "signal", mcpTool.MCP())

	builtin, ok := gated[1].(interface{ MCP() string })
	require.True(t, ok)
	require.Empty(t, builtin.MCP(), "a gated built-in reports no MCP server")
}

func TestRoleGateKeepsChannelFiltering(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	gated := gateToolsForRole(&roles.Role{Name: "researcher", Tools: []string{"mcp_*", "view"}}, []fantasy.AgentTool{
		&roleMCPTool{name: "mcp_signal_send", server: "signal"},
		&roleMCPTool{name: "mcp_switchboard_send", server: "switchboard"},
		roleRecordingTool("view", ran),
	})
	states := map[string]mcp.ClientInfo{"signal": {Channel: true}, "switchboard": {Channel: true}}

	// A switchboard turn sees its own server's tools plus the built-in.
	filtered := filterToolsForChannel(gated, "switchboard", states)
	require.Equal(t, []string{"mcp_switchboard_send", "view"}, toolNames(filtered))

	// A local turn keeps every tool.
	require.Len(t, filterToolsForChannel(gated, "", states), 3)
}

func TestRoleGateKeepsDisabledMCPFiltering(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	gated := gateToolsForRole(&roles.Role{Name: "researcher", Tools: []string{"mcp_*", "view"}}, []fantasy.AgentTool{
		&roleMCPTool{name: "mcp_off_send", server: "off"},
		roleRecordingTool("view", ran),
	})

	filtered := dropDisabledMCPTools(gated, map[string]struct{}{"off": {}})
	require.Equal(t, []string{"view"}, toolNames(filtered), "a disabled server's gated tool must be dropped")

	filtered = dropDisabledMCPTools(gated, map[string]struct{}{"other": {}})
	require.Equal(t, []string{"mcp_off_send", "view"}, toolNames(filtered))
}

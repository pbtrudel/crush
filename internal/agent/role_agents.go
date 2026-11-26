package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

// teamCoordinationTools pass every role gate: a reviewer still has to claim
// its task and report to the Lead.
var teamCoordinationTools = []string{
	SendMessageToolName,
	ListAgentsToolName,
	WaitAgentToolName,
	TeamTaskCreateToolName,
	TeamTaskListToolName,
	TeamTaskGetToolName,
	TeamTaskUpdateToolName,
}

// roleGatedTool refuses calls the role does not allow. It keeps the inner
// tool's Info and provider options, so the tools block a role agent sends
// stays byte-identical to an agent without a role and the cache is shared.
type roleGatedTool struct {
	inner fantasy.AgentTool
	role  *roles.Role
}

// gateToolsForRole wraps every tool in list with the role's gate. A nil
// role returns list unchanged, so agents without a role are untouched.
func gateToolsForRole(role *roles.Role, list []fantasy.AgentTool) []fantasy.AgentTool {
	if role == nil {
		return list
	}
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if gated, ok := tool.(*roleGatedTool); ok {
			tool = gated.inner
		}
		out[i] = &roleGatedTool{inner: tool, role: role}
	}
	return out
}

func (g *roleGatedTool) Info() fantasy.ToolInfo { return g.inner.Info() }

// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so MCP filtering sees through the role gate.
func (g *roleGatedTool) MCP() string {
	if mcpTool, ok := g.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}

func (g *roleGatedTool) ProviderOptions() fantasy.ProviderOptions {
	return g.inner.ProviderOptions()
}

func (g *roleGatedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	g.inner.SetProviderOptions(opts)
}

func (g *roleGatedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	name := g.inner.Info().Name
	if g.role.Allows(name) || slices.Contains(teamCoordinationTools, name) {
		return g.inner.Run(ctx, call)
	}
	return fantasy.NewTextErrorResponse(fmt.Sprintf(
		"tool %q is not available to role %q. Allowed tools: %s",
		name, g.role.Name, strings.Join(g.role.Tools, ", "),
	)), nil
}

// withRoleBlock puts the role block in front of the post-compaction state
// text, so an agent keeps its role after its first message is summarized.
func withRoleBlock(role *roles.Role, state string) string {
	if role == nil {
		return state
	}
	return roles.Block(*role) + state
}

// configuredRoles returns every role agent in cfg.
func configuredRoles(cfg *config.Config) []roles.Role {
	if cfg == nil {
		return nil
	}
	var out []roles.Role
	for _, agent := range cfg.Agents {
		if agent.Role != nil {
			out = append(out, *agent.Role)
		}
	}
	return out
}

// resolveRole looks a role up by name. The string is a refusal for the
// model when the name is unknown.
func resolveRole(cfg *config.Config, name string) (*roles.Role, string) {
	name = strings.TrimSpace(name)
	if cfg != nil {
		if agent, ok := cfg.Agents[name]; ok && agent.Role != nil {
			role := *agent.Role
			return &role, ""
		}
	}
	var names []string
	for _, role := range configuredRoles(cfg) {
		names = append(names, role.Name)
	}
	sort.Strings(names)
	return nil, fmt.Sprintf("unknown role %q; available: %s", name, strings.Join(names, ", "))
}

// roleModelParams turns a role's model group into dispatch override params.
func roleModelParams(role *roles.Role) AgentDispatchParams {
	return AgentDispatchParams{Model: role.Model, Provider: role.Provider, Reasoning: role.Reasoning}
}

// roleCatalog is the role list appended to the delegation tools'
// descriptions. It changes only when config reloads, so the tools block
// stays byte-stable within a session.
func roleCatalog(cfg *config.Config) string {
	list := configuredRoles(cfg)
	if len(list) == 0 {
		return ""
	}
	return "\n\nRoles (pass one as `role`):\n" + strings.TrimRight(roles.Catalog(list), "\n")
}

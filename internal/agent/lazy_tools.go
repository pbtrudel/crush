package agent

import (
	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

// lazyCoreTools is the palette a lazy-tools request always carries: the tools a
// turn needs to read, edit, search and run a codebase, plus the delegation and
// team tools. Everything else is deferred behind tool_search (see
// tools.LazyRegistry).
var lazyCoreTools = map[string]struct{}{
	tools.ViewToolName:       {},
	tools.ReadToolName:       {},
	tools.EditToolName:       {},
	tools.BashToolName:       {},
	tools.GrepToolName:       {},
	tools.GlobToolName:       {},
	tools.LSToolName:         {},
	tools.TodosToolName:      {},
	tools.ToolSearchToolName: {},

	// Delegation and team tools stay in the core palette: they are how a turn
	// hands work off, and a Lead that has to search before it can spawn a
	// teammate spends a step to save nothing.
	AgentToolName:          {},
	AgentDispatchToolName:  {},
	AgentSendToolName:      {},
	AgentStatusToolName:    {},
	AgentStopToolName:      {},
	SpawnTeammateToolName:  {},
	SendMessageToolName:    {},
	ListAgentsToolName:     {},
	WaitAgentToolName:      {},
	InterruptAgentToolName: {},
	TeamTaskCreateToolName: {},
	TeamTaskListToolName:   {},
	TeamTaskGetToolName:    {},
	TeamTaskUpdateToolName: {},
}

// lazyRegistry returns the coordinator's lazy-tool registry, creating it on
// first use. One registry per coordinator means a session's reveals survive the
// tool rebuild a config reload triggers.
func (c *coordinator) lazyRegistry() *tools.LazyRegistry {
	c.lazyMu.Lock()
	defer c.lazyMu.Unlock()
	if c.lazyTools == nil {
		c.lazyTools = tools.NewLazyRegistry(lazyCoreTools)
	}
	return c.lazyTools
}

// lazyRegistryIfEnabled returns the registry only when options.lazy_tools is
// on, so an agent built with the option off holds no registry at all and every
// request keeps the full palette.
func (c *coordinator) lazyRegistryIfEnabled() *tools.LazyRegistry {
	if c.cfg == nil || !c.cfg.Config().Options.GetLazyTools() {
		return nil
	}
	return c.lazyRegistry()
}

// lazyToolsForSession narrows a session's tool list to what lazy mode allows:
// the core palette, MCP tools (their schemas are the server's business, and
// mcp_servers already owns activating them) and whatever this session revealed
// with tool_search. It is a no-op unless the option is on, which is what keeps
// lazy mode off byte-identical to the full palette.
func (a *sessionAgent) lazyToolsForSession(sessionID string, list []fantasy.AgentTool) []fantasy.AgentTool {
	if a.lazyTools == nil {
		return list
	}
	if a.cfg == nil || !a.cfg.Config().Options.GetLazyTools() {
		return list
	}
	registry := a.lazyTools()
	if registry == nil {
		return list
	}
	return registry.Filter(sessionID, list)
}

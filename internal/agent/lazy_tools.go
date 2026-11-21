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
	tools.ApplyPatchToolName: {},
	tools.WriteToolName:      {},
	tools.MultiEditToolName:  {},
	tools.BashToolName:       {},
	tools.GrepToolName:       {},
	tools.GlobToolName:       {},
	tools.LSToolName:         {},
	tools.TodosToolName:      {},
	tools.WorktreeToolName:   {},
	tools.ToolSearchToolName: {},

	// A shell command returns job ids, so the tools that read and kill those
	// jobs have to be there when it does; deferring them would strand every
	// background run behind a search.
	tools.JobOutputToolName: {},
	tools.JobKillToolName:   {},

	// question is how a turn asks the user to choose. A deferred question
	// would make the model guess instead of asking, which is the one cost
	// this option must not add.
	tools.QuestionToolName: {},

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
// the core palette and whatever this session revealed with tool_search. MCP
// tools are deferred like any other non-core tool, since a connected server's
// schemas cost the same tokens on every request. It is a no-op unless the
// option is on, which is what keeps lazy mode off byte-identical to the full
// palette.
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

// lazyPaletteNames collects the tool names an agent's built palette carries.
// tool_search takes them as its scope: the shared registry knows every tool any
// agent built, and only these can enter this agent's requests.
func lazyPaletteNames(list []fantasy.AgentTool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Info().Name)
	}
	return names
}

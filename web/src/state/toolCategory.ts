export type ToolCategory = 'read' | 'write' | 'run' | 'network';

// The table follows the tool names in internal/agent/tools. Anything not
// listed, including agent and team tools, counts as run. It is a Map so a
// name such as "constructor" never resolves through the object prototype.
// mcp_servers is a local tool (internal/agent/tools/mcp_servers.go) that
// only reads server state, so it is listed here and wins over the mcp_
// prefix rule below.
const TABLE = new Map<string, ToolCategory>(Object.entries({
  view: 'read', read: 'read', grep: 'read', glob: 'read', ls: 'read',
  lsp_diagnostics: 'read', lsp_references: 'read', lsp_definition: 'read',
  lsp_symbols: 'read', lsp_call_hierarchy: 'read', tool_search: 'read',
  mcp_servers: 'read',
  edit: 'write', multiedit: 'write', write: 'write', apply_patch: 'write',
  lsp_rename: 'write', lsp_replace_symbol: 'write',
  bash: 'run', job_output: 'run', job_kill: 'run',
  fetch: 'network', agentic_fetch: 'network', download: 'network',
  web_fetch: 'network', web_search: 'network', sourcegraph: 'network',
  read_mcp_resource: 'network', list_mcp_resources: 'network',
} as Record<string, ToolCategory>));

export function toolCategory(name: string): ToolCategory {
  const n = name.toLowerCase();
  const local = TABLE.get(n);
  if (local) return local;
  // Tools from MCP servers are named mcp_<server>_<tool>.
  if (n.startsWith('mcp_')) return 'network';
  return 'run';
}

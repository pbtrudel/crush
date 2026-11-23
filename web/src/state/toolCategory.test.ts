import { describe, expect, it } from 'vitest';
import { toolCategory } from './toolCategory';

describe('toolCategory', () => {
  it.each([
    ['view', 'read'], ['read', 'read'], ['grep', 'read'], ['glob', 'read'], ['ls', 'read'],
    ['lsp_diagnostics', 'read'], ['lsp_references', 'read'], ['lsp_definition', 'read'],
    ['lsp_symbols', 'read'], ['lsp_call_hierarchy', 'read'], ['tool_search', 'read'],
    ['mcp_servers', 'read'],
    ['edit', 'write'], ['multiedit', 'write'], ['write', 'write'], ['apply_patch', 'write'],
    ['lsp_rename', 'write'], ['lsp_replace_symbol', 'write'],
    ['bash', 'run'], ['job_output', 'run'], ['job_kill', 'run'],
    ['fetch', 'network'], ['agentic_fetch', 'network'], ['download', 'network'],
    ['web_fetch', 'network'], ['web_search', 'network'], ['sourcegraph', 'network'],
    ['mcp_github_create_issue', 'network'], ['read_mcp_resource', 'network'],
    ['list_mcp_resources', 'network'],
  ])('%s is %s', (name, want) => {
    expect(toolCategory(name)).toBe(want);
  });

  it('counts unknown tools as run', () => {
    expect(toolCategory('agent_dispatch')).toBe('run');
    expect(toolCategory('')).toBe('run');
  });

  it('ignores case', () => {
    expect(toolCategory('View')).toBe('read');
  });

  it('does not read names off the object prototype', () => {
    expect(toolCategory('constructor')).toBe('run');
    expect(toolCategory('toString')).toBe('run');
    expect(toolCategory('__proto__')).toBe('run');
  });

  it('looks local tools up before the mcp_ prefix rule', () => {
    // mcp_servers is a local tool that only reads server state.
    expect(toolCategory('mcp_servers')).toBe('read');
    expect(toolCategory('mcp_other_server_tool')).toBe('network');
  });
});

import { describe, expect, it } from 'vitest';
import { buildItems, buildSkillToggles, filterItems, type PaletteSources } from './items';

const sources: PaletteSources = {
  model: {
    id: 'm1', name: 'Model One', provider: 'p1', context_window: 1000,
    can_reason: true, effort_levels: [], think: false, agent_ready: true,
  },
  busy: false,
  hasSession: true,
  commands: [
    { id: 'user:fix', title: 'user:fix', kind: 'custom', args: [{ id: 'FILE', title: 'FILE', required: true }] },
    { id: 'skill:go-tests', title: 'go-tests', kind: 'skill', args: [] },
  ],
  prompts: [{ id: 'p1', client_id: 'github', prompt_id: 'summarize_pr', title: 'Summarize PR', args: [] }],
  skills: [
    { name: 'go-tests', description: 'Write Go tests', disabled: false },
    { name: 'retired', description: 'No longer used', disabled: true },
  ],
  sessions: [{ id: 's-2', title: 'Fix the lexer' }],
  models: [{ provider: 'p1', provider_name: 'Provider One', id: 'm2', name: 'Model Two', context_window: 1000, can_reason: false }],
  yolo: 'normal',
  plan: 'code',
  cinematic: false,
  motion: 'full',
};

describe('palette items', () => {
  it('builds every group in source order', () => {
    const groups = new Set(buildItems(sources).map((i) => i.group));
    expect([...groups]).toEqual(['Actions', 'Commands', 'Skills', 'MCP prompts', 'Sessions', 'Models', 'Toggles']);
  });

  it('leaves skill commands out of Commands and disabled skills out of Skills', () => {
    const items = buildItems(sources);
    expect(items.filter((i) => i.group === 'Commands').map((i) => i.title)).toEqual(['user:fix']);
    expect(items.filter((i) => i.group === 'Skills').map((i) => i.title)).toEqual(['go-tests']);
  });

  it('offers the thinking toggle only for a model that reasons without effort levels', () => {
    const items = buildItems(sources);
    expect(items.find((i) => i.id === 'toggle:thinking')?.title).toBe('Enable thinking');
    expect(items.find((i) => i.id === 'toggle:thinking')?.hint).toBe('Off');
    expect(buildItems({ ...sources, model: { ...sources.model, effort_levels: ['low'] } }).some((i) => i.id === 'toggle:thinking')).toBe(false);
    expect(buildItems({ ...sources, model: { ...sources.model, can_reason: false } }).some((i) => i.id === 'toggle:thinking')).toBe(false);
  });

  it('offers the reasoning level only for a model that can reason', () => {
    expect(buildItems(sources).some((i) => i.id === 'action:reasoning-level')).toBe(true);
    const level = buildItems(sources).find((i) => i.id === 'action:reasoning-level')!;
    expect(level.title).toBe('Reasoning level');
    expect(level.keywords).toEqual(['reasoning', 'effort', 'think']);
    expect(buildItems({ ...sources, model: { ...sources.model, can_reason: false } })
      .some((i) => i.id === 'action:reasoning-level')).toBe(false);
  });

  it('offers the systems panel', () => {
    const item = buildItems(sources).find((i) => i.id === 'action:systems')!;
    expect(item.title).toBe('Systems');
    expect(item.keywords).toEqual(['mcp', 'lsp', 'servers', 'systems']);
    expect(item.run).toEqual({ kind: 'action', action: 'systems' });
  });

  it('hides session actions without a session', () => {
    const ids = buildItems({ ...sources, hasSession: false }).map((i) => i.id);
    expect(ids).not.toContain('action:compact');
  });

  it('shows Toggle skills only when a skill exists', () => {
    expect(buildItems(sources).some((i) => i.id === 'action:toggle-skills')).toBe(true);
    expect(buildItems({ ...sources, skills: [] }).some((i) => i.id === 'action:toggle-skills')).toBe(false);
  });

  it('ranks an exact keyword above a title prefix above a word start above a subsequence', () => {
    const items = buildItems(sources);
    const ranked = filterItems(items, 'summarize').map((i) => i.id);
    expect(ranked[0]).toBe('action:compact');
    expect(ranked.indexOf('mcp:github:summarize_pr')).toBeGreaterThan(0);

    const ses = filterItems(items, 'ses').map((i) => i.id);
    expect(ses[0]).toBe('action:sessions');
    expect(ses.indexOf('action:compact')).toBeLessThan(ses.indexOf('action:display'));

    expect(filterItems(items, 'comp')[0].id).toBe('action:compact');
    expect(filterItems(items, 'zzz')).toEqual([]);
  });

  it('matches aliases as keywords', () => {
    const items = buildItems(sources);
    expect(filterItems(items, 'clear')[0].id).toBe('action:new-session');
    expect(filterItems(items, 'reload-skills')[0].id).toBe('action:reload-skills');
  });

  it('shows a skill as text and keeps its enabled state out of the main list', () => {
    const items = buildItems(sources);
    const skill = items.find((i) => i.id === 'skill:go-tests')!;
    expect(skill.run).toEqual({ kind: 'skill', name: 'go-tests' });
    expect(skill.hint).toBe('Write Go tests');
  });

  it('lists every skill with its enabled state in the skills section', () => {
    const toggles = buildSkillToggles(sources.skills);
    expect(toggles.map((i) => i.title)).toEqual(['go-tests', 'retired']);
    expect(toggles.map((i) => i.hint)).toEqual(['Enabled', 'Disabled']);
    expect(toggles[1].run).toEqual({ kind: 'skill-toggle', name: 'retired' });
  });
});

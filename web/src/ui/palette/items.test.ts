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
    { name: 'go-tests', description: 'Write Go tests', disabled: false, disabled_globally: false },
    { name: 'retired', description: 'No longer used', disabled: true, disabled_globally: false },
  ],
  sessions: [{ id: 's-2', title: 'Fix the lexer' }],
  models: [{ provider: 'p1', provider_name: 'Provider One', id: 'm2', name: 'Model Two', context_window: 1000, can_reason: false }],
  yolo: 'normal',
  plan: 'code',
  cinematic: false,
  cinematicAllowed: false,
  motion: 'full',
  agentTeams: false,
  agentTeamsLoaded: true,
  docker: { available: true, enabled: false },
  hasChannels: true,
  browsing: false,
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

  it('offers the sign-in dialog', () => {
    const item = buildItems(sources).find((i) => i.id === 'action:sign-in')!;
    expect(item.title).toBe('Sign in to a provider');
    expect(item.keywords).toEqual(['login', 'api key', 'oauth', 'provider', 'auth']);
    expect(item.run).toEqual({ kind: 'action', action: 'sign-in' });
  });

  it('offers the shell command, the note and the side question', () => {
    const items = buildItems(sources);
    const shell = items.find((i) => i.id === 'action:run-shell')!;
    expect(shell.title).toBe('Run shell command');
    expect(shell.run).toEqual({ kind: 'action', action: 'run-shell' });

    const note = items.find((i) => i.id === 'action:add-note')!;
    expect(note.title).toBe('Add note');
    expect(note.run).toEqual({ kind: 'action', action: 'add-note' });

    const ask = items.find((i) => i.id === 'action:ask-side')!;
    expect(ask.title).toBe('Ask side question');
    expect(ask.args).toEqual([{ id: 'question', title: 'Question', required: true }]);

    const noSession = buildItems({ ...sources, hasSession: false }).map((i) => i.id);
    expect(noSession).toContain('action:run-shell');
    expect(noSession).not.toContain('action:add-note');
    expect(noSession).not.toContain('action:ask-side');
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

  it('lists the M4 commands', () => {
    const items = buildItems(sources);
    const titles = items.map((i) => i.title);
    for (const title of [
      'Export session as Markdown', 'Set goal', 'Goal status', 'Clear goal',
      'Start loop', 'Loop status', 'Clear loops', 'Initialize project', 'Stop web mode',
    ]) {
      expect(titles).toContain(title);
    }
    expect(items.find((i) => i.id === 'action:set-goal')!.args)
      .toEqual([{ id: 'objective', title: 'Objective', required: true }]);
    expect(items.find((i) => i.id === 'action:start-loop')!.args)
      .toEqual([{ id: 'task', title: 'Task', required: true }]);

    const noSession = buildItems({ ...sources, hasSession: false }).map((i) => i.title);
    for (const title of [
      'Export session as Markdown', 'Set goal', 'Goal status', 'Clear goal',
      'Start loop', 'Loop status', 'Clear loops',
    ]) {
      expect(noSession).not.toContain(title);
    }
    expect(noSession).toContain('Initialize project');
    expect(noSession).toContain('Stop web mode');
  });

  it('offers Disable Docker MCP whenever it is enabled', () => {
    const titles = buildItems({ ...sources, docker: { available: false, enabled: true } }).map((i) => i.title);
    expect(titles).toContain('Disable Docker MCP catalog');
    expect(titles).not.toContain('Enable Docker MCP catalog');
  });

  it('offers Docker MCP by state', () => {
    const off = buildItems(sources).map((i) => i.title);
    expect(off).toContain('Enable Docker MCP catalog');
    expect(off).not.toContain('Disable Docker MCP catalog');

    const on = buildItems({ ...sources, docker: { available: true, enabled: true } }).map((i) => i.title);
    expect(on).toContain('Disable Docker MCP catalog');
    expect(on).not.toContain('Enable Docker MCP catalog');

    const gone = buildItems({ ...sources, docker: { available: false, enabled: false } }).map((i) => i.title);
    expect(gone).not.toContain('Enable Docker MCP catalog');
    expect(gone).not.toContain('Disable Docker MCP catalog');
  });

  it('toggles Agent Teams with its state in the title', () => {
    const off = buildItems(sources).find((i) => i.id === 'toggle:agent-teams')!;
    expect(off.title).toBe('Agent Teams: Off');
    expect(off.run).toEqual({ kind: 'action', action: 'toggle-agent-teams' });
    expect(buildItems({ ...sources, agentTeams: true }).find((i) => i.id === 'toggle:agent-teams')!.title)
      .toBe('Agent Teams: On');
  });

  it('hides Agent Teams until the config loads', () => {
    const hidden = buildItems({ ...sources, agentTeamsLoaded: false }).map((i) => i.id);
    expect(hidden).not.toContain('toggle:agent-teams');
    expect(buildItems({ ...sources, agentTeamsLoaded: true, agentTeams: true })
      .find((i) => i.id === 'toggle:agent-teams')!.title).toBe('Agent Teams: On');
  });

  it('offers Channels only when a server could be one', () => {
    const item = buildItems(sources).find((i) => i.id === 'action:channels')!;
    expect(item.title).toBe('Channels');
    expect(item.run).toEqual({ kind: 'action', action: 'channels' });
    expect(buildItems({ ...sources, hasChannels: false }).some((i) => i.id === 'action:channels')).toBe(false);
  });

  it('offers a global toggle beside each skill', () => {
    const toggles = buildSkillToggles([
      { name: 'go-tests', description: 'Write Go tests', disabled: false, disabled_globally: false },
      { name: 'crush-config', description: 'Read the config', disabled: true, disabled_globally: true },
    ]);
    const local = toggles.filter((i) => i.id === 'skill-global:go-tests');
    expect(local.map((i) => i.title)).toEqual(['Disable go-tests everywhere']);
    expect(local[0].run).toEqual({ kind: 'skill-global', name: 'go-tests', disabled: true });

    const global = toggles.find((i) => i.id === 'skill-global:crush-config')!;
    expect(global.title).toBe('Enable crush-config everywhere');
    expect(global.run).toEqual({ kind: 'skill-global', name: 'crush-config', disabled: false });
  });

  it('shows a skill as text and keeps its enabled state out of the main list', () => {
    const items = buildItems(sources);
    const skill = items.find((i) => i.id === 'skill:go-tests')!;
    expect(skill.run).toEqual({ kind: 'skill', name: 'go-tests' });
    expect(skill.hint).toBe('Write Go tests');
  });

  it('lists every skill with its enabled state in the skills section', () => {
    const toggles = buildSkillToggles(sources.skills).filter((i) => i.id.startsWith('skill-toggle:'));
    expect(toggles.map((i) => i.title)).toEqual(['go-tests', 'retired']);
    expect(toggles.map((i) => i.hint)).toEqual(['Enabled', 'Disabled']);
    expect(toggles[1].run).toEqual({ kind: 'skill-toggle', name: 'retired' });
  });

  it('offers the cinematic toggle only when the scene allows it', () => {
    const ids = buildItems({ ...sources, cinematicAllowed: true }).map((i) => i.id);
    expect(ids).toContain('toggle:cinematic');
    expect(ids).not.toContain('action:cinematic');

    const off = buildItems({ ...sources, cinematicAllowed: false }).map((i) => i.id);
    expect(off).not.toContain('toggle:cinematic');
    expect(off).not.toContain('action:cinematic');
  });
});

describe('browsing another session', () => {
  const HIDDEN = [
    'action:new-session', 'action:init-project', 'action:compact', 'action:ask-side', 'action:add-note',
    'action:run-shell', 'action:set-goal', 'action:start-loop',
  ];

  it('hides the actions that start agent work or move the terminal', () => {
    const ids = buildItems({ ...sources, browsing: true }).map((i) => i.id);
    for (const id of HIDDEN) expect(ids).not.toContain(id);
    expect(ids).toContain('action:export-session');
    expect(ids).toContain('action:goal-status');
    expect(ids).toContain('action:clear-goal');
    expect(ids).toContain('action:loop-status');
    expect(ids).toContain('action:clear-loops');
    expect(ids).toContain('action:stop-web');
    // The sessions stay listed so the tab can browse another one.
    expect(ids).toContain('session:s-2');
  });

  it('hides commands, MCP prompts and skills while browsing', () => {
    const items = buildItems({ ...sources, browsing: true });
    expect(items.filter((i) => i.group === 'Commands')).toEqual([]);
    expect(items.filter((i) => i.group === 'Skills')).toEqual([]);
    expect(items.filter((i) => i.group === 'MCP prompts')).toEqual([]);
    // Sessions and models stay: they are how the tab browses and reads.
    expect(items.some((i) => i.group === 'Sessions')).toBe(true);
    expect(items.some((i) => i.group === 'Models')).toBe(true);
  });

  it('keeps them while following the terminal', () => {
    const ids = buildItems(sources).map((i) => i.id);
    for (const id of HIDDEN) expect(ids).toContain(id);
  });
});

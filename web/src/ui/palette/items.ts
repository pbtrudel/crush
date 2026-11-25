import type {
  CommandArg, CommandEntry, DockerMcp, InputMode, McpPrompt, ModelChoice, ModelInfo, PermissionMode, Session,
  SkillEntry,
} from '../../api/types';
import { showThinkingToggle } from '../../state/model';
import type { Motion } from '../../state/motion';

export type PaletteGroup =
  | 'Actions' | 'Commands' | 'Skills' | 'MCP prompts' | 'Sessions' | 'Models' | 'Toggles';

export type PaletteRun =
  | { kind: 'action'; action: string }
  | { kind: 'command'; id: string }
  | { kind: 'mcp'; clientId: string; promptId: string }
  | { kind: 'session'; id: string }
  | { kind: 'model'; provider: string; model: string }
  | { kind: 'skill'; name: string }
  | { kind: 'skill-toggle'; name: string }
  | { kind: 'skill-global'; name: string; disabled: boolean };

export interface PaletteItem {
  id: string;
  group: PaletteGroup;
  title: string;
  hint?: string;
  keywords: string[];
  args?: CommandArg[];
  run: PaletteRun;
}

export interface PaletteSources {
  model: ModelInfo;
  busy: boolean;
  hasSession: boolean;
  commands: CommandEntry[];
  prompts: McpPrompt[];
  skills: SkillEntry[];
  sessions: Pick<Session, 'id' | 'title'>[];
  models: ModelChoice[];
  yolo: PermissionMode;
  plan: InputMode;
  cinematic: boolean;
  cinematicAllowed: boolean;
  motion: Motion;
  agentTeams: boolean;
  agentTeamsLoaded: boolean;
  docker: DockerMcp;
  hasChannels: boolean;
  // browsing hides the actions that start agent work or move the terminal's
  // session while the tab reads another one.
  browsing: boolean;
}

// The action ids the palette dispatches. Each one maps to a TUI command.
export const ACTIONS = {
  newSession: 'new-session',
  sessions: 'sessions',
  model: 'model',
  reasoningLevel: 'reasoning-level',
  systems: 'systems',
  signIn: 'sign-in',
  compact: 'compact',
  runShell: 'run-shell',
  addNote: 'add-note',
  askSide: 'ask-side',
  reloadSkills: 'reload-skills',
  toggleSkills: 'toggle-skills',
  drawer: 'drawer',
  display: 'display',
  reloadConfig: 'reload-config',
  thinking: 'toggle-thinking',
  yolo: 'toggle-yolo',
  plan: 'toggle-plan',
  cinematicToggle: 'toggle-cinematic',
  motionToggle: 'cycle-motion',
  exportSession: 'export-session',
  setGoal: 'set-goal',
  goalStatus: 'goal-status',
  clearGoal: 'clear-goal',
  startLoop: 'start-loop',
  loopStatus: 'loop-status',
  clearLoops: 'clear-loops',
  agentTeams: 'toggle-agent-teams',
  dockerEnable: 'docker-enable',
  dockerDisable: 'docker-disable',
  channels: 'channels',
  initProject: 'init-project',
  stopWeb: 'stop-web',
} as const;

// BROWSING_HIDDEN names the actions a browsing tab cannot run: they start
// agent work in a session, or make the terminal follow one.
const BROWSING_HIDDEN: ReadonlySet<string> = new Set([
  ACTIONS.newSession, ACTIONS.initProject, ACTIONS.compact, ACTIONS.askSide, ACTIONS.addNote,
  ACTIONS.runShell, ACTIONS.setGoal, ACTIONS.startLoop,
]);

function action(id: string, title: string, keywords: string[], hint?: string, args?: CommandArg[]): PaletteItem {
  return { id: `action:${id}`, group: 'Actions', title, keywords, args, run: { kind: 'action', action: id }, hint };
}

function toggle(id: string, title: string, hint: string, keywords: string[], act: string): PaletteItem {
  return { id: `toggle:${id}`, group: 'Toggles', title, hint, keywords, run: { kind: 'action', action: act } };
}

function onOff(on: boolean): string {
  return on ? 'On' : 'Off';
}

export function buildItems(s: PaletteSources): PaletteItem[] {
  const actions: PaletteItem[] = [
    action(ACTIONS.newSession, 'New session', ['clear']),
    action(ACTIONS.sessions, 'Sessions', []),
    action(ACTIONS.model, 'Switch model', ['model']),
    action(ACTIONS.systems, 'Systems', ['mcp', 'lsp', 'servers', 'systems']),
    action(ACTIONS.signIn, 'Sign in to a provider', ['login', 'api key', 'oauth', 'provider', 'auth']),
  ];
  if (s.hasChannels) actions.push(action(ACTIONS.channels, 'Channels', ['channels', 'mcp', 'discord']));
  if (s.docker.enabled) {
    actions.push(action(ACTIONS.dockerDisable, 'Disable Docker MCP catalog', ['docker', 'mcp', 'catalog']));
  } else if (s.docker.available) {
    actions.push(action(ACTIONS.dockerEnable, 'Enable Docker MCP catalog', ['docker', 'mcp', 'catalog']));
  }
  if (s.model.can_reason) {
    actions.push(action(ACTIONS.reasoningLevel, 'Reasoning level', ['reasoning', 'effort', 'think']));
  }
  actions.push(action(ACTIONS.runShell, 'Run shell command', ['shell', 'command', 'bang']));
  if (s.hasSession) {
    actions.push(
      action(ACTIONS.compact, 'Compact session', ['summarize']),
      action(ACTIONS.exportSession, 'Export session as Markdown', ['export', 'markdown', 'download']),
      action(ACTIONS.setGoal, 'Set goal', ['goal'], undefined, [
        { id: 'objective', title: 'Objective', required: true },
      ]),
      action(ACTIONS.goalStatus, 'Goal status', ['goal', 'status']),
      action(ACTIONS.clearGoal, 'Clear goal', ['goal', 'clear']),
      action(ACTIONS.startLoop, 'Start loop', ['loop', 'start'], undefined, [
        { id: 'task', title: 'Task', required: true },
      ]),
      action(ACTIONS.loopStatus, 'Loop status', ['loop', 'status']),
      action(ACTIONS.clearLoops, 'Clear loops', ['loop', 'clear']),
      action(ACTIONS.addNote, 'Add note', ['note', 'remember', 'keep']),
      action(ACTIONS.askSide, 'Ask side question', ['ask', 'question', 'side'], undefined, [
        { id: 'question', title: 'Question', required: true },
      ]),
    );
  }
  actions.push(action(ACTIONS.reloadSkills, 'Reload skills', ['reload-skills']));
  if (s.skills.length > 0) actions.push(action(ACTIONS.toggleSkills, 'Toggle skills', ['skills']));
  actions.push(
    action(ACTIONS.drawer, 'Open file drawer', ['drawer', 'files']),
    action(ACTIONS.display, 'Display settings', ['display', 'motion', 'settings']),
    action(ACTIONS.reloadConfig, 'Reload configuration', ['reload', 'reload-config', 'config']),
    action(ACTIONS.initProject, 'Initialize project', ['init', 'initialize', 'project', 'onboarding']),
    action(ACTIONS.stopWeb, 'Stop web mode', ['stop', 'web', 'detach']),
  );

  const commands: PaletteItem[] = s.commands
    .filter((c) => c.kind === 'custom')
    .map((c) => ({
      id: `command:${c.id}`,
      group: 'Commands',
      title: c.title,
      hint: c.description,
      keywords: c.id === c.title ? [] : [c.id],
      args: c.args,
      run: { kind: 'command', id: c.id },
    }));

  const skills: PaletteItem[] = s.skills
    .filter((k) => !k.disabled)
    .map((k) => ({
      id: `skill:${k.name}`,
      group: 'Skills',
      title: k.name,
      hint: k.description,
      keywords: [],
      run: { kind: 'skill', name: k.name },
    }));

  const prompts: PaletteItem[] = s.prompts.map((p) => ({
    id: `mcp:${p.client_id}:${p.prompt_id}`,
    group: 'MCP prompts',
    title: p.title,
    hint: p.description,
    keywords: [p.prompt_id],
    args: p.args,
    run: { kind: 'mcp', clientId: p.client_id, promptId: p.prompt_id },
  }));

  const sessions: PaletteItem[] = s.sessions.map((sess) => ({
    id: `session:${sess.id}`,
    group: 'Sessions',
    title: sess.title || 'Untitled session',
    keywords: [],
    run: { kind: 'session', id: sess.id },
  }));

  const models: PaletteItem[] = s.models.map((m) => ({
    id: `model:${m.provider}:${m.id}`,
    group: 'Models',
    title: m.name || m.id,
    hint: m.provider_name,
    keywords: [m.provider, m.id],
    run: { kind: 'model', provider: m.provider, model: m.id },
  }));

  const toggles: PaletteItem[] = [];
  if (showThinkingToggle(s.model)) {
    toggles.push(toggle(
      'thinking',
      s.model.think ? 'Disable thinking' : 'Enable thinking',
      onOff(!!s.model.think),
      ['thinking', 'reason'],
      ACTIONS.thinking,
    ));
  }
  toggles.push(
    toggle('yolo', 'Yolo permissions', onOff(s.yolo === 'yolo'), ['yolo', 'permissions'], ACTIONS.yolo),
    toggle('plan', 'Plan mode', onOff(s.plan === 'plan'), ['plan', 'mode'], ACTIONS.plan),
  );
  // Without a scene there is nothing to fly, so the palette does not offer a
  // toggle that the App would refuse.
  if (s.cinematicAllowed) {
    toggles.push(toggle('cinematic', 'Cinematic mode', onOff(s.cinematic), ['cinematic'], ACTIONS.cinematicToggle));
  }
  toggles.push(toggle('motion', 'Motion', s.motion, ['motion', 'reduce', 'effects'], ACTIONS.motionToggle));
  if (s.agentTeamsLoaded) {
    toggles.push({
      id: 'toggle:agent-teams',
      group: 'Toggles',
      title: `Agent Teams: ${onOff(s.agentTeams)}`,
      keywords: ['agent', 'teams', 'toggle-agent-teams'],
      run: { kind: 'action', action: ACTIONS.agentTeams },
    });
  }

  const shown = s.browsing
    ? actions.filter((i) => !(i.run.kind === 'action' && BROWSING_HIDDEN.has(i.run.action)))
    : actions;
  // Commands, skills and MCP prompts start work in the session the palette
  // would name; a browsing tab offers none of them.
  return [
    ...shown,
    ...(s.browsing ? [] : commands),
    ...(s.browsing ? [] : skills),
    ...(s.browsing ? [] : prompts),
    ...sessions, ...models, ...toggles,
  ];
}

// buildSkillToggles lists every skill, disabled ones included, so the skills
// section can turn one back on. The main list leaves disabled skills out.
export function buildSkillToggles(skills: SkillEntry[]): PaletteItem[] {
  return skills.flatMap((k) => [
    {
      id: `skill-toggle:${k.name}`,
      group: 'Skills' as const,
      title: k.name,
      hint: k.disabled ? 'Disabled' : 'Enabled',
      keywords: k.description ? [k.description] : [],
      run: { kind: 'skill-toggle' as const, name: k.name },
    },
    {
      id: `skill-global:${k.name}`,
      group: 'Skills' as const,
      title: `${k.disabled_globally ? 'Enable' : 'Disable'} ${k.name} everywhere`,
      keywords: k.description ? [k.description] : [],
      run: { kind: 'skill-global' as const, name: k.name, disabled: !k.disabled_globally },
    },
  ]);
}

function words(title: string): string[] {
  return title.toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
}

function subsequence(text: string, query: string): boolean {
  let at = 0;
  for (const ch of text) {
    if (ch === query[at]) at++;
    if (at === query.length) return true;
  }
  return false;
}

// rank orders a match the way the palette offers it: an exact keyword (a
// TUI alias) first, then a title prefix, then a word start or keyword
// prefix, then a subsequence of the title. Anything else does not match.
function rank(item: PaletteItem, query: string): number | null {
  const title = item.title.toLowerCase();
  const keywords = item.keywords.map((k) => k.toLowerCase());
  if (keywords.includes(query)) return 0;
  if (title.startsWith(query)) return 1;
  if (words(title).some((w) => w.startsWith(query))) return 2;
  if (keywords.some((k) => k.startsWith(query))) return 2;
  if (subsequence(title, query)) return 3;
  return null;
}

export function filterItems(items: PaletteItem[], query: string): PaletteItem[] {
  const q = query.trim().toLowerCase();
  if (!q) return items.slice();
  const scored: { item: PaletteItem; rank: number; at: number }[] = [];
  items.forEach((item, at) => {
    const r = rank(item, q);
    if (r !== null) scored.push({ item, rank: r, at });
  });
  scored.sort((a, b) => a.rank - b.rank || a.at - b.at);
  return scored.map((s) => s.item);
}

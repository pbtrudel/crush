import type {
  CommandArg, CommandEntry, InputMode, McpPrompt, ModelChoice, ModelInfo, PermissionMode, Session,
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
  | { kind: 'skill-toggle'; name: string };

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
  motion: Motion;
}

// The action ids the palette dispatches. Each one maps to a TUI command.
export const ACTIONS = {
  newSession: 'new-session',
  sessions: 'sessions',
  model: 'model',
  compact: 'compact',
  reloadSkills: 'reload-skills',
  toggleSkills: 'toggle-skills',
  drawer: 'drawer',
  cinematic: 'cinematic',
  display: 'display',
  reloadConfig: 'reload-config',
  thinking: 'toggle-thinking',
  yolo: 'toggle-yolo',
  plan: 'toggle-plan',
  cinematicToggle: 'toggle-cinematic',
  motionToggle: 'cycle-motion',
} as const;

function action(id: string, title: string, keywords: string[], hint?: string): PaletteItem {
  return { id: `action:${id}`, group: 'Actions', title, keywords, run: { kind: 'action', action: id }, hint };
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
  ];
  if (s.hasSession) actions.push(action(ACTIONS.compact, 'Compact session', ['summarize']));
  actions.push(action(ACTIONS.reloadSkills, 'Reload skills', ['reload-skills']));
  if (s.skills.length > 0) actions.push(action(ACTIONS.toggleSkills, 'Toggle skills', ['skills']));
  actions.push(
    action(ACTIONS.drawer, 'Open file drawer', ['drawer', 'files']),
    action(ACTIONS.cinematic, 'Toggle cinematic mode', ['cinematic', 'fly']),
    action(ACTIONS.display, 'Display settings', ['display', 'motion', 'settings']),
    action(ACTIONS.reloadConfig, 'Reload configuration', ['reload', 'reload-config', 'config']),
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
    toggle('cinematic', 'Cinematic mode', onOff(s.cinematic), ['cinematic'], ACTIONS.cinematicToggle),
    toggle('motion', 'Motion', s.motion, ['motion', 'reduce', 'effects'], ACTIONS.motionToggle),
  );

  return [...actions, ...commands, ...skills, ...prompts, ...sessions, ...models, ...toggles];
}

// buildSkillToggles lists every skill, disabled ones included, so the skills
// section can turn one back on. The main list leaves disabled skills out.
export function buildSkillToggles(skills: SkillEntry[]): PaletteItem[] {
  return skills.map((k) => ({
    id: `skill-toggle:${k.name}`,
    group: 'Skills',
    title: k.name,
    hint: k.disabled ? 'Disabled' : 'Enabled',
    keywords: k.description ? [k.description] : [],
    run: { kind: 'skill-toggle', name: k.name },
  }));
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

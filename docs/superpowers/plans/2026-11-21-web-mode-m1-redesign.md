# Web Mode M1 Redesign (Neon Holodeck) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the muted first look of the web mode tab with the neon
holographic language of spec section 3: a three.js world behind glass and
hologram panels, HUD instruments, a frame-time guard, cinematic mode, a scene
boot sequence, and screenshot plus axe checks.

**Architecture:** The scene moves out of the center column into a fixed,
full-viewport layer behind every panel. It is split into small three.js
builders (world, core, bursts) that can be constructed and inspected in jsdom
without a WebGL context, plus one mount module that owns the renderer, bloom
composer, render loop and frame-time guard. Everything the HUD and the scene
derive from the stream (tool categories, tool events, usage samples) lives in
pure, unit-tested state modules.

**Tech Stack:** Preact 11 with `@preact/signals`, three 0.187
(`three/addons/postprocessing/*`, `three/addons/controls/OrbitControls.js`),
Vite 8, Vitest 5 (jsdom), Playwright 1.65, `@axe-core/playwright` (new dev
dependency, Task 11 only).

**Spec:** `docs/superpowers/specs/2026-11-21-web-mode-design.md`, section 3
(lines 270 to 596). Read section 3 in full before any task.

## Global Constraints

- Worktree: `/home/pbtrudel/git/.crush-worktrees/web-mode`, branch
  `crush/web-mode`. Work only there; absolute paths for file tools,
  `working_dir` for bash. Never touch `/home/pbtrudel/git/crush`.
- Every go and playwright command is prefixed with
  `GOCACHE=/tmp/crush-go-build` (the default cache is unwritable).
- Playwright needs `LD_LIBRARY_PATH=/tmp/alsa-stub`; never commit that stub.
- CSP is `default-src 'self'; img-src 'self' data: blob:; style-src 'self'`.
  No `style=` attributes in markup and no inline `<style>`. Setting
  properties through the CSSOM (`el.style.transform = ...`) is allowed.
  `grep -c 'style=' internal/web/dist/index.html` must print 0.
- No new runtime dependencies. The only new dev dependency is
  `@axe-core/playwright` (Task 11).
- Token values, verbatim from spec 3.2: void `oklch(0.12 0.05 275)`, haze
  `oklch(0.30 0.16 300)`, glass `oklch(0.16 0.05 265 / 0.70)`, glass solid
  `oklch(0.16 0.05 265 / 0.94)`, holo cyan `oklch(0.86 0.17 205)`, magenta
  `oklch(0.70 0.29 340)`, violet `oklch(0.62 0.25 295)`, teal
  `oklch(0.82 0.16 170)`, amber `oklch(0.83 0.17 75)`, text
  `oklch(0.95 0.02 220)`, muted `oklch(0.74 0.05 240)`.
- Tool category colors: read cyan, write teal, run amber, network violet.
  Unknown tools count as run. Magenta is reserved for "needs you" and errors.
- Context thresholds: amber from 0.75, magenta from 0.90.
- Render rates: 60 fps while anything event-driven moves, 30 fps for idle
  ambient motion only, paused when `document.hidden`. Motion `off` renders on
  state change only.
- Frame-time guard: average over 25 ms across about 3 s steps down one level
  (bloom, then beams, then half the particles), at most one step per 10 s,
  toast "Reduced effects to keep things smooth" with a restore button.
- About 1,500 particles in one `Points` draw call. Low power: no bloom, no
  beams, a quarter of the particles, no cinematic mode.
- Motion `reduced`: ambient scene motion at a quarter speed, no parallax, no
  flicker or glitch, bursts become a single core brightness pulse.
- Cinematic toggle: `event.code === 'KeyC'` with `altKey`, `preventDefault`.
  Esc exits only when focus is outside the prompt and no interrupt dialog is
  open. Flight about 900 ms expo-out; reduced or off motion: 150 ms fade.
- Every component and file added here keeps the DOM order of the log, the
  stage section, the deck and the interrupt layer unchanged.
- Toasts dismiss after 6 s; error toasts stay until dismissed.
- Initial load at most about 600 KB gzipped; three.js, postprocessing, the
  cinematic code and OrbitControls load as separate chunks.
- Code style: TypeScript strict, existing file patterns, no em dashes in any
  source or doc line, own-line comments are full sentences ending in a period
  and wrapped at 78 columns, and only where the reason is not obvious.
- Each task ends with the full check run, then one commit that includes the
  rebuilt `internal/web/dist`:

```bash
cd /home/pbtrudel/git/.crush-worktrees/web-mode/web && npm run typecheck && npm test && npm run build
cd .. && GOCACHE=/tmp/crush-go-build go build ./... && grep -c 'style=' internal/web/dist/index.html
git diff --cached -- . ':!internal/web/dist' ':!web/package-lock.json' | grep -c $'^+.*—'
```

  The last two commands must print 0 (the em dash check runs after
  `git add`). Visual tasks (3 to 10) also take a screenshot with the lead's
  `cur.cjs` script and look at it before committing; the commands are in the
  Screenshot section at the end.

## Review Focus

1. A reconnect or session reload delivers a snapshot full of old tool calls:
   no burst storm in the scene and no spike in the activity chart (Task 2
   test "a snapshot baselines tool calls").
2. The tab is hidden for minutes, then shown: the first frame after the gap
   must not count as a slow frame or as a huge animation step (Task 7 tests
   "ignores gaps" and "clamps the step").
3. The WebGL context is lost mid-session (GPU reset, driver crash): the tab
   falls back to the 2D view and stays usable (Task 7 test "falls back on
   context loss").
4. `Alt+C` while typing in the prompt toggles cinematic mode without
   inserting a character, and Esc inside the prompt or inside a dialog never
   leaves cinematic mode (Task 10 key tests).
5. The context window is unknown (0) or tokens exceed it: the gauge shows
   0 % or 100 %, never NaN, and the level thresholds still hold (Task 8 test
   "clamps the gauge").

---

### Task 1: Neon tokens and backdrop

**Files:**
- Modify: `web/src/styles/tokens.css`
- Modify: `web/src/styles/base.css:3-23`
- Modify: `web/src/scene/palette.ts` (fallback hex values only, through its
  callers in `web/src/scene/constellation.ts:41-44`)
- Test: `web/src/styles/tokens.test.ts`

**Interfaces:**
- Produces CSS custom properties used by every later task: `--void`,
  `--void-deep`, `--haze`, `--glass`, `--glass-solid`, `--surface`,
  `--surface-solid`, `--surface-raised`, `--edge`, `--edge-strong`, `--cyan`,
  `--cyan-dim`, `--magenta`, `--magenta-dim`, `--violet`, `--violet-dim`,
  `--teal`, `--amber`, `--text`, `--text-muted`, `--glow-cyan`,
  `--glow-magenta`, `--glow-violet`, `--glow-text`, `--blur-glass`.
  Old names stay so existing stylesheets keep working; they now point at the
  new palette.

- [ ] **Step 1: Write the failing test.** Replace `tokenValues` in
  `tokens.test.ts` with the new contract and add the glow checks:

```ts
const tokenValues: Record<string, string> = {
  '--void': 'oklch(0.12 0.05 275)',
  '--void-deep': 'oklch(0.09 0.04 275)',
  '--haze': 'oklch(0.30 0.16 300)',
  '--glass': 'oklch(0.16 0.05 265 / 0.70)',
  '--glass-solid': 'oklch(0.16 0.05 265 / 0.94)',
  '--surface': 'var(--glass)',
  '--surface-solid': 'var(--glass-solid)',
  '--surface-raised': 'oklch(0.21 0.06 262 / 0.82)',
  '--edge': 'oklch(0.86 0.17 205 / 0.35)',
  '--edge-strong': 'oklch(0.86 0.17 205 / 0.7)',
  '--cyan': 'oklch(0.86 0.17 205)',
  '--cyan-dim': 'oklch(0.86 0.17 205 / 0.35)',
  '--magenta': 'oklch(0.70 0.29 340)',
  '--magenta-dim': 'oklch(0.70 0.29 340 / 0.35)',
  '--violet': 'oklch(0.62 0.25 295)',
  '--violet-dim': 'oklch(0.62 0.25 295 / 0.3)',
  '--teal': 'oklch(0.82 0.16 170)',
  '--amber': 'oklch(0.83 0.17 75)',
  '--text': 'oklch(0.95 0.02 220)',
  '--text-muted': 'oklch(0.74 0.05 240)',
  '--blur-glass': '14px',
  // Unchanged type, spacing and timing tokens stay in the table as before.
  '--font-hud': "'Chakra Petch', system-ui, sans-serif",
  '--font-prose': "'IBM Plex Sans', system-ui, sans-serif",
  '--font-code': "'JetBrains Mono', ui-monospace, monospace",
  '--step--1': '0.8125rem',
  '--step-0': '0.9375rem',
  '--step-1': '1.125rem',
  '--step-2': '1.5rem',
  '--step-3': '2rem',
  '--space-1': '4px',
  '--space-4': '16px',
  '--space-9': '36px',
  '--radius': '6px',
  '--dur-in': '320ms',
  '--dur-out': '220ms',
};
```

  and in `describe('design tokens')`:

```ts
  it('builds each glow from a tight edge and an 18px halo at 35 percent', () => {
    const css = collapse(tokens);
    expect(css).toContain('--glow-cyan: 0 0 0 1px oklch(0.86 0.17 205), 0 0 18px oklch(0.86 0.17 205 / 0.35);');
    expect(css).toContain('--glow-magenta: 0 0 0 1px oklch(0.70 0.29 340), 0 0 18px oklch(0.70 0.29 340 / 0.35);');
    expect(css).toContain('--glow-violet: 0 0 0 1px oklch(0.62 0.25 295), 0 0 18px oklch(0.62 0.25 295 / 0.35);');
    expect(css).toContain('--glow-text: 0 0 8px oklch(0.86 0.17 205 / 0.6);');
  });
```

  In `describe('base styles')` replace the background test:

```ts
  it('shades the page from void at the top to haze at the horizon', () => {
    const css = collapse(base);
    expect(css).toContain('linear-gradient(180deg, var(--void) 0%, var(--void) 45%, var(--haze) 100%)');
    // The scene draws its own floor grid, so the CSS grid overlay is gone.
    expect(base).not.toContain('background-size: 36px 36px');
  });
```

- [ ] **Step 2: Run it and see it fail.**
  `cd web && npx vitest run src/styles/tokens.test.ts` fails on
  `--void: oklch(0.12 0.05 275);`.
- [ ] **Step 3: Implement.** Rewrite the color block of `tokens.css` with the
  values above (keep the type, spacing, radius and easing blocks), replace the
  glow lines with the four glow tokens, add `--blur-glass: 14px;`. In
  `base.css` set `body { background: linear-gradient(180deg, var(--void) 0%,
  var(--void) 45%, var(--haze) 100%) fixed; ... }` and delete the
  `body::before` grid rule. Update the four `token()` fallback hex values in
  `constellation.ts` to the sRGB of the new tokens: cyan `#2fe4ff`, violet
  `#8a4dff`, magenta `#ff2fb4`, amber `#ffb02e` (the comment in `palette.ts`
  says fallbacks equal the token; check each with `parseOklch` in a scratch
  Vitest run and use the rounded hex it prints).
- [ ] **Step 4: Run** `npx vitest run src/styles` (pass) and the full check
  run.
- [ ] **Step 5: Screenshot and commit** `feat(web): adopt the neon token
  palette`.

---

### Task 2: Telemetry from the stream (tool categories, tool events, usage)

**Files:**
- Create: `web/src/state/toolCategory.ts`, `web/src/state/toolCategory.test.ts`
- Create: `web/src/state/telemetry.ts`, `web/src/state/telemetry.test.ts`
- Create: `web/src/state/levels.ts`, `web/src/state/levels.test.ts`
- Modify: `web/src/state/store.ts` (`handleFrames` and `reloadSession`)
- Test: `web/src/state/store.test.ts` (one new case)

**Interfaces:**
- Produces:
  - `type ToolCategory = 'read' | 'write' | 'run' | 'network'`
  - `toolCategory(name: string): ToolCategory`
  - `interface ToolEvent { id: string; category: ToolCategory; at: number }`
  - `interface UsageSample { at: number; tokens: number; cost: number }`
  - `interface Telemetry { seen: ReadonlySet<string>; tools: ToolEvent[]; usage: UsageSample[] }`
  - `TOOL_WINDOW_MS = 15 * 60_000`, `USAGE_WINDOW_MS = 10 * 60_000`
  - `emptyTelemetry(): Telemetry`
  - `observe(t: Telemetry, prev: State, next: State, baseline: boolean, now: number): Telemetry`
  - `telemetry` (a `signal<Telemetry>` exported from `telemetry.ts`)
  - `type ContextLevel = 'ok' | 'warn' | 'critical'`, `contextLevel(fill: number): ContextLevel`
  - `type CoreMode = 'idle' | 'working' | 'attention' | 'error'`, `coreMode(s: State): CoreMode`

- [ ] **Step 1: Write the failing tests.**

`toolCategory.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { toolCategory } from './toolCategory';

describe('toolCategory', () => {
  it.each([
    ['view', 'read'], ['read', 'read'], ['grep', 'read'], ['glob', 'read'], ['ls', 'read'],
    ['lsp_diagnostics', 'read'], ['lsp_references', 'read'], ['lsp_definition', 'read'],
    ['lsp_symbols', 'read'], ['lsp_call_hierarchy', 'read'], ['tool_search', 'read'],
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
});
```

`levels.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { initialState, type State } from './reducer';
import { contextLevel, coreMode } from './levels';
import type { Message } from '../api/types';

const msg = (over: Partial<Message>): Message => ({
  id: 'm', role: 'assistant', session_id: 's', parts: [], model: '', provider: '',
  created_at: 1, updated_at: 1, ...over,
});

describe('contextLevel', () => {
  it('turns amber at 75 percent and magenta at 90 percent', () => {
    expect(contextLevel(0)).toBe('ok');
    expect(contextLevel(0.749)).toBe('ok');
    expect(contextLevel(0.75)).toBe('warn');
    expect(contextLevel(0.899)).toBe('warn');
    expect(contextLevel(0.9)).toBe('critical');
    expect(contextLevel(1)).toBe('critical');
  });

  it('treats NaN as empty', () => {
    expect(contextLevel(Number.NaN)).toBe('ok');
  });
});

describe('coreMode', () => {
  const base: State = { ...initialState(), conn: 'live', sessionId: 's' };

  it('is idle by default and working while busy', () => {
    expect(coreMode(base)).toBe('idle');
    expect(coreMode({ ...base, busy: true })).toBe('working');
  });

  it('needs you while a permission or question waits, even when busy', () => {
    const perm = { id: 'p', session_id: 's', tool_call_id: 't', tool_name: 'bash', description: '', action: '', params: {}, path: '' };
    expect(coreMode({ ...base, busy: true, permissions: [perm] })).toBe('attention');
  });

  it('shows an error when the last turn finished with an error', () => {
    const failed = msg({ parts: [{ type: 'finish', data: { reason: 'error', time: 1 } }] });
    expect(coreMode({ ...base, messages: [failed] })).toBe('error');
    expect(coreMode({ ...base, busy: true, messages: [failed] })).toBe('working');
  });
});
```

`telemetry.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { initialState, type State } from './reducer';
import { emptyTelemetry, observe, TOOL_WINDOW_MS, USAGE_WINDOW_MS } from './telemetry';
import type { Message, Session } from '../api/types';

const call = (id: string, name: string) => ({ type: 'tool_call' as const, data: { id, name, input: '{}' } });
const asst = (id: string, ...ids: [string, string][]): Message => ({
  id, role: 'assistant', session_id: 's', parts: ids.map(([c, n]) => call(c, n)),
  model: '', provider: '', created_at: 1, updated_at: 1,
});
const session = (tokens: number, cost: number): Session => ({
  id: 's', parent_session_id: '', title: '', message_count: 0, prompt_tokens: tokens,
  completion_tokens: 0, cost, is_busy: false, created_at: 0, updated_at: 0,
});
const live: State = { ...initialState(), conn: 'live', sessionId: 's' };

describe('observe', () => {
  it('records a new tool call with its category and the observation time', () => {
    const next = { ...live, messages: [asst('a', ['t1', 'edit'])] };
    const t = observe(emptyTelemetry(), live, next, false, 1000);
    expect(t.tools).toEqual([{ id: 't1', category: 'write', at: 1000 }]);
  });

  it('records a call once even as its message keeps updating', () => {
    const one = { ...live, messages: [asst('a', ['t1', 'view'])] };
    const two = { ...live, messages: [asst('a', ['t1', 'view'], ['t2', 'bash'])] };
    let t = observe(emptyTelemetry(), live, one, false, 1000);
    t = observe(t, one, two, false, 2000);
    expect(t.tools.map((e) => e.id)).toEqual(['t1', 't2']);
  });

  it('a snapshot baselines tool calls', () => {
    const snap = { ...live, messages: [asst('a', ['t1', 'view'], ['t2', 'bash'])] };
    let t = observe(emptyTelemetry(), initialState(), snap, true, 1000);
    expect(t.tools).toEqual([]);
    // A reconnect snapshot with the same history records nothing either.
    t = observe(t, snap, snap, true, 2000);
    expect(t.tools).toEqual([]);
    expect(t.seen.has('t2')).toBe(true);
  });

  it('drops tool events older than the window', () => {
    const one = { ...live, messages: [asst('a', ['t1', 'view'])] };
    const two = { ...live, messages: [asst('a', ['t1', 'view'], ['t2', 'grep'])] };
    let t = observe(emptyTelemetry(), live, one, false, 0);
    t = observe(t, one, two, false, TOOL_WINDOW_MS + 1);
    expect(t.tools.map((e) => e.id)).toEqual(['t2']);
  });

  it('samples usage when the session totals change, and only then', () => {
    const a = { ...live, session: session(100, 0.01) };
    const b = { ...live, session: session(250, 0.02) };
    let t = observe(emptyTelemetry(), live, a, false, 1000);
    t = observe(t, a, a, false, 1500);
    t = observe(t, a, b, false, 2000);
    expect(t.usage).toEqual([
      { at: 1000, tokens: 100, cost: 0.01 },
      { at: 2000, tokens: 250, cost: 0.02 },
    ]);
  });

  it('keeps the last usage sample older than the window as the sparkline start', () => {
    const a = { ...live, session: session(100, 0.01) };
    const b = { ...live, session: session(200, 0.02) };
    const c = { ...live, session: session(300, 0.03) };
    let t = observe(emptyTelemetry(), live, a, false, 0);
    t = observe(t, a, b, false, 1000);
    t = observe(t, b, c, false, USAGE_WINDOW_MS + 2000);
    expect(t.usage.map((u) => u.tokens)).toEqual([200, 300]);
  });

  it('starts fresh when the session changes', () => {
    const a = { ...live, messages: [asst('a', ['t1', 'view'])], session: session(100, 0.01) };
    let t = observe(emptyTelemetry(), live, a, false, 1000);
    const other = { ...live, sessionId: 'x', messages: [] };
    t = observe(t, a, other, false, 2000);
    expect(t).toEqual(emptyTelemetry());
  });
});
```

Add to `store.test.ts` (follow the file's existing setup for `handleFrames`):

```ts
it('feeds telemetry and treats a snapshot batch as baseline', () => {
  // Use the events fixture the file already loads: apply its snapshot frame,
  // then a message frame that adds one new tool call.
  // Expect telemetry.value.tools to hold exactly that one call.
});
```

  The implementer writes this case against the real fixture shape in
  `src/state/__fixtures__/events.json`; the assertion is the one in the
  comment: after a snapshot frame plus a live message frame adding tool call
  `X`, `telemetry.value.tools.map((e) => e.id)` equals `['X']`.

- [ ] **Step 2: Run** `npx vitest run src/state` and see the new files fail
  to import.
- [ ] **Step 3: Implement.**

`toolCategory.ts`:

```ts
export type ToolCategory = 'read' | 'write' | 'run' | 'network';

// The table follows the tool names in internal/agent/tools. Anything not
// listed, including agent and team tools, counts as run.
const TABLE: Record<string, ToolCategory> = {
  view: 'read', read: 'read', grep: 'read', glob: 'read', ls: 'read',
  lsp_diagnostics: 'read', lsp_references: 'read', lsp_definition: 'read',
  lsp_symbols: 'read', lsp_call_hierarchy: 'read', tool_search: 'read',
  edit: 'write', multiedit: 'write', write: 'write', apply_patch: 'write',
  lsp_rename: 'write', lsp_replace_symbol: 'write',
  bash: 'run', job_output: 'run', job_kill: 'run',
  fetch: 'network', agentic_fetch: 'network', download: 'network',
  web_fetch: 'network', web_search: 'network', sourcegraph: 'network',
  read_mcp_resource: 'network', list_mcp_resources: 'network',
};

export function toolCategory(name: string): ToolCategory {
  const n = name.toLowerCase();
  if (n.startsWith('mcp_')) return 'network';
  return TABLE[n] ?? 'run';
}
```

`levels.ts`:

```ts
import { needsAttention, type State } from './reducer';

export type ContextLevel = 'ok' | 'warn' | 'critical';
export type CoreMode = 'idle' | 'working' | 'attention' | 'error';

export function contextLevel(fill: number): ContextLevel {
  if (!(fill >= 0.75)) return 'ok';
  return fill >= 0.9 ? 'critical' : 'warn';
}

export function coreMode(s: State): CoreMode {
  if (needsAttention(s)) return 'attention';
  if (s.busy) return 'working';
  const last = s.messages.at(-1);
  const failed = last?.role === 'assistant' && last.parts.some((p) => p.type === 'finish' && p.data.reason === 'error');
  return failed ? 'error' : 'idle';
}
```

`telemetry.ts`:

```ts
import { signal } from '@preact/signals';
import type { State } from './reducer';
import { toolCategory, type ToolCategory } from './toolCategory';

export interface ToolEvent { id: string; category: ToolCategory; at: number }
export interface UsageSample { at: number; tokens: number; cost: number }
export interface Telemetry { seen: ReadonlySet<string>; tools: ToolEvent[]; usage: UsageSample[] }

export const TOOL_WINDOW_MS = 15 * 60_000;
export const USAGE_WINDOW_MS = 10 * 60_000;

export function emptyTelemetry(): Telemetry {
  return { seen: new Set(), tools: [], usage: [] };
}

export const telemetry = signal<Telemetry>(emptyTelemetry());

// observe derives the HUD history from consecutive states. A baseline
// observation (a snapshot) marks the calls it holds as seen without
// recording them, so a reconnect never replays history as new activity.
export function observe(t: Telemetry, prev: State, next: State, baseline: boolean, now: number): Telemetry {
  if (prev.sessionId !== next.sessionId) t = emptyTelemetry();
  let seen = t.seen as Set<string>;
  let tools = t.tools;
  if (next.messages !== prev.messages || baseline) {
    for (const m of next.messages) {
      for (const p of m.parts) {
        if (p.type !== 'tool_call' || seen.has(p.data.id)) continue;
        if (seen === t.seen) seen = new Set(t.seen);
        seen.add(p.data.id);
        if (!baseline) tools = [...tools, { id: p.data.id, category: toolCategory(p.data.name), at: now }];
      }
    }
  }
  tools = tools.filter((e) => now - e.at <= TOOL_WINDOW_MS);
  let usage = t.usage;
  const sess = next.session;
  if (sess) {
    const tokens = sess.prompt_tokens + sess.completion_tokens;
    const last = usage.at(-1);
    if (!last || last.tokens !== tokens || last.cost !== sess.cost) usage = [...usage, { at: now, tokens, cost: sess.cost }];
  }
  const firstInside = usage.findIndex((u) => now - u.at <= USAGE_WINDOW_MS);
  if (firstInside > 1) usage = usage.slice(firstInside - 1);
  else if (firstInside === -1 && usage.length > 1) usage = usage.slice(-1);
  if (seen === t.seen && tools.length === t.tools.length && usage === t.usage) return t;
  return { seen, tools, usage };
}
```

  The implementer must make the tests pass; adjust the window trimming if a
  test disagrees, never the test's expected values.

In `store.ts`, wrap state application so every path feeds telemetry. Add:

```ts
import { observe, telemetry } from './telemetry';

function apply(fn: (s: State) => State, baseline: boolean) {
  const prev = state.value;
  const next = fn(prev);
  state.value = next;
  telemetry.value = observe(telemetry.value, prev, next, baseline, Date.now());
}
```

  `handleFrames` calls `apply((s) => frames.reduce(reduce, s), frames.some((f) => f.type === 'snapshot'))`
  instead of `dispatch`, and `reloadSession` calls `apply(..., true)` for its
  snapshot. `dispatch` itself stays for actions that do not touch messages.
- [ ] **Step 4: Run** `npx vitest run src/state` (pass), then the full check
  run.
- [ ] **Step 5: Commit** `feat(web): derive tool activity and usage history
  from the stream`.

---

### Task 3: Glass and hologram panels, full-viewport scene layer, motion CSS

**Files:**
- Create: `web/src/styles/panels.css`, `web/src/styles/panels.test.ts`
- Create: `web/src/ui/stage/SceneLayer.tsx`
- Create: `web/src/state/effects.ts`, `web/src/state/effects.test.ts`
- Modify: `web/src/main.tsx` (import `panels.css` after `base.css`)
- Modify: `web/src/ui/App.tsx`, `web/src/ui/stage/Stage.tsx`,
  `web/src/ui/Toasts.tsx`, `web/src/state/reducer.ts` (Toast type and
  `pushToast`), `web/src/styles/shell.css`, `web/src/styles/stage.css`,
  `web/src/styles/deck.css`, `web/src/styles/interrupt.css`,
  `web/src/styles/log.css`, `web/src/ui/interrupt/PermissionDialog.tsx`,
  `web/src/ui/interrupt/QuestionForm.tsx`, `web/src/ui/deck/CommandDeck.tsx`,
  `web/src/ui/Hud.tsx` (class names only)
- Test: `web/src/ui/Toasts.test.tsx`, `web/src/ui/stage/Stage.test.tsx`,
  a new `web/src/ui/App.test.tsx`

**Interfaces:**
- Consumes: Task 1 tokens.
- Produces:
  - CSS classes: `.glass`, `.glass--alert`, `.holo`, `.brackets` (corner
    brackets on any positioned element), `.scanlines`, `.enter` (power-up
    entrance), `.flicker-once`.
  - `state/effects.ts`:
    `type EffectsLevel = 0 | 1 | 2 | 3`,
    `effectsLevel: Signal<EffectsLevel>`, `sceneFailed: Signal<boolean>`,
    `webglSupported(): boolean` (probe once, cached),
    `use3D(): boolean` (reads `webglSupported()`, `lowPower`, `sceneFailed`),
    `interface EffectFlags { bloom: boolean; beams: boolean; particleShare: number; cinematic: boolean }`,
    `effectFlags(level: EffectsLevel, lowPower: boolean): EffectFlags`,
    `restoreEffects(): void`.
  - `stageRect: Signal<DOMRectReadOnly | null>` exported from
    `ui/stage/Stage.tsx`'s new sibling `state/layout.ts`.
  - `Toast` gains `action?: 'restore-effects'`; `pushToast(s, tone, text, action?)`.
  - `SceneLayer` renders `<canvas class="scene-layer__canvas">` when
    `use3D()`; Stage renders the SVG fallback inside `.stage` otherwise.

- [ ] **Step 1: Write the failing tests.**

`effects.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { effectFlags } from './effects';

describe('effectFlags', () => {
  it('steps down bloom, then beams, then half the particles', () => {
    expect(effectFlags(0, false)).toEqual({ bloom: true, beams: true, particleShare: 1, cinematic: true });
    expect(effectFlags(1, false)).toEqual({ bloom: false, beams: true, particleShare: 1, cinematic: true });
    expect(effectFlags(2, false)).toEqual({ bloom: false, beams: false, particleShare: 1, cinematic: true });
    expect(effectFlags(3, false)).toEqual({ bloom: false, beams: false, particleShare: 0.5, cinematic: true });
  });

  it('low power wins over any level', () => {
    for (const level of [0, 1, 2, 3] as const) {
      expect(effectFlags(level, true)).toEqual({ bloom: false, beams: false, particleShare: 0.25, cinematic: false });
    }
  });
});
```

`panels.test.ts` (same disk-reading pattern as `tokens.test.ts`):

```ts
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('./panels.css', import.meta.url), 'utf8').replace(/\s+/g, ' ');

describe('panel primitives', () => {
  it('blurs glass and falls back to the solid token without backdrop-filter', () => {
    expect(css).toContain('.glass {');
    expect(css).toContain('backdrop-filter: blur(var(--blur-glass));');
    expect(css).toContain('@supports not (backdrop-filter: blur(1px))');
    expect(css).toContain('background: var(--glass-solid);');
  });

  it('gives alert glass magenta edges and brackets', () => {
    expect(css).toContain('.glass--alert {');
    expect(css).toMatch(/\.glass--alert \{[^}]*--bracket: var\(--magenta\)/);
  });

  it('draws holograms without a fill and flickers them once', () => {
    expect(css).toMatch(/\.holo \{[^}]*background: oklch\(0\.12 0\.05 275 \/ 0\.2\)/);
    expect(css).toContain('@keyframes flicker-once');
    expect(css).toContain('@keyframes power-up');
  });

  it('turns off flicker and glitch for reduced motion', () => {
    expect(css).toMatch(/\[data-motion='reduced'\] \.flicker-once[^{]*\{[^}]*animation-name: fade-in/);
  });
});
```

`App.test.tsx`:

```ts
import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState } from '../state/reducer';
import { state } from '../state/store';
import { App } from './App';

vi.mock('../scene/constellation', () => ({ mountConstellation: () => { throw new Error('no webgl'); } }));

describe('App layout', () => {
  it('puts reading surfaces on glass and glanceable ones on hologram, in the same DOM order', async () => {
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    await act(() => { render(<App />, root); });
    expect(root.querySelector('.hud')!.classList.contains('holo')).toBe(true);
    expect(root.querySelector('.panel--log')!.classList.contains('glass')).toBe(true);
    expect(root.querySelector('.holodeck__deck .deck')!.classList.contains('glass')).toBe(true);
    const order = Array.from(root.querySelectorAll('.hud, .panel--log, .stage, .holodeck__deck')).map((e) => e.className.split(' ')[0]);
    expect(order).toEqual(['hud', 'panel', 'stage', 'holodeck__deck']);
    render(null, root);
  });
});
```

  (If Task 7 later renames `scene/constellation`, that task updates the
  mock path.)

`Toasts.test.tsx`, add:

```ts
it('keeps an error until dismissed and clears others after 6 s', async () => {
  vi.useFakeTimers();
  // Push one info and one error toast through the reducer helpers, mount,
  // advance 6001 ms: the info toast is gone, the error toast remains.
});

it('offers a restore button on the reduced-effects toast', async () => {
  // pushToast(s, 'warn', 'Reduced effects to keep things smooth', 'restore-effects')
  // renders a "Restore effects" button; clicking it sets effectsLevel to 0
  // and dismisses the toast.
});
```

  The implementer fills both bodies following the file's existing mount
  helpers; the assertions are as written in the comments.

`Stage.test.tsx`: keep both existing tests (they cover the SVG fallback) and
point them at the new split: Stage renders the SVG when `use3D()` is false.

- [ ] **Step 2: Run** `npx vitest run` and see the new tests fail.
- [ ] **Step 3: Implement.**

`panels.css` primitives (the implementer may tune numbers, not structure):

```css
.glass {
  --bracket: var(--cyan);
  position: relative;
  background: var(--glass);
  backdrop-filter: blur(var(--blur-glass));
  border: 1px solid transparent;
  border-radius: var(--radius);
  /* The two-tone edge: cyan at the top left fading to violet. */
  background-image: linear-gradient(var(--glass), var(--glass)),
    linear-gradient(135deg, var(--cyan), var(--violet));
  background-origin: border-box;
  background-clip: padding-box, border-box;
  box-shadow: 0 0 18px oklch(0.86 0.17 205 / 0.35);
}
@supports not (backdrop-filter: blur(1px)) {
  .glass { background: var(--glass-solid); background-image: linear-gradient(var(--glass-solid), var(--glass-solid)), linear-gradient(135deg, var(--cyan), var(--violet)); }
}
.glass--alert {
  --bracket: var(--magenta);
  background-image: linear-gradient(var(--glass-solid), var(--glass-solid)),
    linear-gradient(135deg, var(--magenta), var(--violet));
  box-shadow: var(--glow-magenta);
}
.holo {
  --bracket: var(--cyan);
  position: relative;
  background: oklch(0.12 0.05 275 / 0.2);
  border: 0;
  color: var(--text);
  text-shadow: var(--glow-text);
}
/* Four L-shaped corners drawn as eight gradient strokes on one layer. */
.glass::after, .holo::after, .brackets::after {
  content: ''; position: absolute; inset: -1px; pointer-events: none;
  --b: 14px; --t: 2px;
  background:
    linear-gradient(var(--bracket), var(--bracket)) top left / var(--b) var(--t),
    linear-gradient(var(--bracket), var(--bracket)) top left / var(--t) var(--b),
    linear-gradient(var(--bracket), var(--bracket)) top right / var(--b) var(--t),
    linear-gradient(var(--bracket), var(--bracket)) top right / var(--t) var(--b),
    linear-gradient(var(--bracket), var(--bracket)) bottom left / var(--b) var(--t),
    linear-gradient(var(--bracket), var(--bracket)) bottom left / var(--t) var(--b),
    linear-gradient(var(--bracket), var(--bracket)) bottom right / var(--b) var(--t),
    linear-gradient(var(--bracket), var(--bracket)) bottom right / var(--t) var(--b);
  background-repeat: no-repeat;
  filter: drop-shadow(0 0 4px var(--bracket));
}
/* Scan lines sit in the panel background, under the content. */
.scanlines::before {
  content: ''; position: absolute; inset: 0; z-index: -1; pointer-events: none;
  border-radius: inherit;
  background: repeating-linear-gradient(0deg, oklch(0.86 0.17 205 / 0.035) 0 1px, transparent 1px 3px);
}
.enter { animation: power-up 280ms var(--ease-out) both; }
.flicker-once { animation: flicker-once 120ms steps(3) 1 both; }
@keyframes power-up {
  0% { opacity: 0; transform: translateY(6px) scaleY(0.96); filter: brightness(2.2) blur(1px); }
  35% { opacity: 0.7; filter: brightness(1.6); }
  55% { opacity: 0.4; }
  100% { opacity: 1; transform: none; filter: none; }
}
@keyframes flicker-once { 0% { opacity: 0; } 33% { opacity: 0.9; } 66% { opacity: 0.3; } 100% { opacity: 1; } }
@keyframes fade-in { from { opacity: 0; } }
[data-motion='reduced'] .enter, [data-motion='reduced'] .flicker-once { animation-name: fade-in; animation-duration: 150ms !important; }
```

  `base.css` already forces 1 ms animations under reduced motion; the
  reduced rule above must win for `.enter` and `.flicker-once` (the
  `!important` duration and the later stylesheet order do that; check it in
  the browser with the Display menu set to Reduced).

Markup changes (class names only, DOM order unchanged):
- `Hud.tsx`: `<header class="hud holo scanlines">`. Remove the old
  `.hud::before` cut-corner layer from `shell.css`.
- `App.tsx`: log section `class="panel panel--log glass scanlines"`; render
  `<SceneLayer />` as the first child of `.holodeck`, before `BootSequence`.
- `CommandDeck.tsx`: the root `class="deck glass"`.
- `PermissionDialog.tsx` and `QuestionForm.tsx` roots: add
  `glass glass--alert enter`; the interrupt layer dims the scene with
  `background: oklch(0.09 0.04 275 / 0.55)`.
- `MessageView` root elements: add `enter` so new messages power up.
- `Toasts.tsx`: each toast `class="toast holo flicker-once"`; timers 6000 ms
  for non-error tones, none for `error`; render
  `<button class="btn btn--ghost">Restore effects</button>` when
  `t.action === 'restore-effects'`, calling `restoreEffects()` then
  `actions.dismissToast(t.id)`. Move `.toasts` to the top right
  (`inset-block-start` below the HUD, `inset-inline-end: var(--space-4)`).
- `log.css`: code blocks (`pre`) sit in a darker inset well inside the glass:
  `background: oklch(0.09 0.04 275 / 0.85)`, a 1 px `--edge` border and an
  inner shadow; inline `code` gets a lighter version of the same.
- Buttons: `.btn--primary` becomes solid cyan with `box-shadow: var(--glow-cyan)`;
  `.btn` (secondary) is a cyan outline that fills with `--cyan-dim` on hover.
- `.hud__select`: `appearance: none`, glass-solid background, cyan edge, a
  chevron drawn with two `linear-gradient` strokes at the right (no data URI),
  options styled `background: var(--void-deep); color: var(--text)`.

Scene layer:
- `SceneLayer.tsx` moves the canvas and its mount logic out of `Stage.tsx`
  unchanged in behavior (still `import('../../scene/constellation')` and
  `mountConstellation(canvas, latest.current)`; Task 7 swaps the module).
  The canvas is `position: fixed; inset: 0; z-index: 0` with
  `aria-hidden="true"`. On a mount failure it sets `sceneFailed.value = true`.
- `.holodeck` gets `position: relative; z-index: 1` and keeps its grid.
  `main.holodeck__grid` drops `perspective`; `.panel--log` uses
  `transform: perspective(1400px) rotateY(6deg)` so no ancestor of the fixed
  canvas creates a containing block. The `.stage` section becomes
  transparent and `pointer-events: none` except its agent list.
- `Stage.tsx` keeps the agent list, renders `ConstellationSvg` only when
  `!use3D()`, and publishes its bounding rect to `stageRect` with a
  `ResizeObserver` plus a `resize` listener (Task 4 centers the projector in
  it).
- `effects.ts` holds `webglSupported` (the old probe from `Stage.tsx`, cached
  in a module variable), `sceneFailed`, `effectsLevel`, `effectFlags`,
  `restoreEffects` and `use3D`. Leaving low power resets `sceneFailed` (the
  old `setFailed(false)` effect).
- [ ] **Step 4: Run** `npx vitest run` (pass), the full check run, and the
  e2e suite (6 passed; update selectors only if a class name changed, never
  weaken an assertion).
- [ ] **Step 5: Screenshot, look at it, commit** `feat(web): glass and
  hologram panels over a full-viewport scene`.

---

### Task 4: Scene world (backdrop, floor grid, mirror, particles, beams, camera)

**Files:**
- Create: `web/src/scene/rng.ts`, `web/src/scene/rng.test.ts`
- Create: `web/src/scene/colors.ts`
- Create: `web/src/scene/world.ts`, `web/src/scene/world.test.ts`
- Create: `web/src/scene/camera.ts`, `web/src/scene/camera.test.ts`

**Interfaces:**
- Consumes: `palette.ts` `token()`; `EffectFlags` from Task 3.
- Produces:
  - `mulberry32(seed: number): () => number`; `sceneSeed(): number` (reads
    `localStorage['crush-web-seed']` when it parses as an integer, else
    `Date.now() >>> 0`).
  - `interface SceneColors { void: THREE.Color; haze: THREE.Color; cyan: THREE.Color; magenta: THREE.Color; violet: THREE.Color; teal: THREE.Color; amber: THREE.Color }`,
    `readColors(): SceneColors` (via `token()` with sRGB fallbacks).
  - `PARTICLE_COUNT = 1500`, `BEAM_COUNT = 4`.
  - `interface World { group: THREE.Group; particles: THREE.Points; beams: THREE.Group; floor: THREE.Mesh; setEffects(f: EffectFlags): void; setReveal(r: number): void; update(dt: number, t: number): void; mirror(source: THREE.Object3D): void; dispose(): void }`
  - `createWorld(colors: SceneColors, seed: number): World`
  - `HOME = { position: new THREE.Vector3(0, 2.2, 7.4), target: new THREE.Vector3(0, 1.0, 0), fov: 42 }`,
    `WIDE = { position: new THREE.Vector3(0, 4.6, 11.5), target: new THREE.Vector3(0, 1.2, 0) }`
  - `viewOffset(viewport: { width: number; height: number }, focus: { x: number; y: number; width: number; height: number } | null): { x: number; y: number }`
  - `parallax(pointer: { x: number; y: number }, motion: Motion): { yaw: number; pitch: number }`

- [ ] **Step 1: Write the failing tests.**

`rng.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { mulberry32 } from './rng';

describe('mulberry32', () => {
  it('repeats for the same seed and stays in [0, 1)', () => {
    const a = mulberry32(42), b = mulberry32(42);
    for (let i = 0; i < 100; i++) {
      const x = a();
      expect(x).toBe(b());
      expect(x).toBeGreaterThanOrEqual(0);
      expect(x).toBeLessThan(1);
    }
  });

  it('differs across seeds', () => {
    expect(mulberry32(1)()).not.toBe(mulberry32(2)());
  });
});
```

`world.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { BEAM_COUNT, createWorld, PARTICLE_COUNT } from './world';
import { readColors } from './colors';

describe('createWorld', () => {
  it('draws every particle in one Points object', () => {
    const w = createWorld(readColors(), 7);
    let points = 0;
    w.group.traverse((o) => { if ((o as THREE.Points).isPoints) points++; });
    expect(points).toBe(1);
    expect(w.particles.geometry.getAttribute('position').count).toBe(PARTICLE_COUNT);
  });

  it('places particles the same way for the same seed', () => {
    const a = createWorld(readColors(), 7).particles.geometry.getAttribute('position').array;
    const b = createWorld(readColors(), 7).particles.geometry.getAttribute('position').array;
    expect(Array.from(a.slice(0, 30))).toEqual(Array.from(b.slice(0, 30)));
  });

  it('applies the effect flags', () => {
    const w = createWorld(readColors(), 7);
    expect(w.beams.children).toHaveLength(BEAM_COUNT);
    w.setEffects({ bloom: false, beams: false, particleShare: 0.25, cinematic: false });
    expect(w.beams.visible).toBe(false);
    expect(w.particles.geometry.drawRange.count).toBe(PARTICLE_COUNT / 4);
    w.setEffects({ bloom: true, beams: true, particleShare: 1, cinematic: true });
    expect(w.beams.visible).toBe(true);
    expect(w.particles.geometry.drawRange.count).toBe(PARTICLE_COUNT);
  });

  it('mirrors a source under the floor and follows its rotation', () => {
    const w = createWorld(readColors(), 7);
    const src = new THREE.Mesh(new THREE.SphereGeometry(0.5), new THREE.MeshBasicMaterial({ transparent: true, opacity: 1 }));
    src.position.y = 1.2;
    w.mirror(src);
    src.rotation.y = 1;
    w.update(0.016, 1);
    const copy = w.group.getObjectByName('mirror')!.children[0];
    expect(copy.position.y).toBeCloseTo(-1.2);
    expect(copy.rotation.y).toBeCloseTo(1);
    expect(((copy as THREE.Mesh).material as THREE.Material).opacity).toBeLessThan(0.5);
  });
});
```

`camera.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { parallax, viewOffset } from './camera';

describe('viewOffset', () => {
  it('is zero without a focus rect', () => {
    expect(viewOffset({ width: 1440, height: 860 }, null)).toEqual({ x: 0, y: 0 });
  });

  it('shifts the view so the projector sits in the middle of the focus rect', () => {
    // Focus centered at x = 1000: the image must move right by 280 px, which
    // is a negative view offset in three.js terms.
    expect(viewOffset({ width: 1440, height: 860 }, { x: 600, y: 100, width: 800, height: 600 })).toEqual({ x: -280, y: -(400 - 430) });
  });
});

describe('parallax', () => {
  it('follows the pointer by at most 2 degrees in full motion', () => {
    const max = (2 * Math.PI) / 180;
    expect(parallax({ x: 1, y: -1 }, 'full')).toEqual({ yaw: max, pitch: -max });
    expect(parallax({ x: 5, y: 0 }, 'full').yaw).toBeCloseTo(max);
  });

  it('is still in reduced and off motion', () => {
    expect(parallax({ x: 1, y: 1 }, 'reduced')).toEqual({ yaw: 0, pitch: 0 });
    expect(parallax({ x: 1, y: 1 }, 'off')).toEqual({ yaw: 0, pitch: 0 });
  });
});
```

- [ ] **Step 2: Run** `npx vitest run src/scene` and see the imports fail.
- [ ] **Step 3: Implement.**
  - `rng.ts`: the standard mulberry32
    (`a |= 0; a = (a + 0x6d2b79f5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296;`).
  - `colors.ts`: `readColors()` calling `token('--void', '#06061c')` and so
    on for each color (fallback hex from `parseOklch` of the token, as in
    Task 1).
  - `world.ts`:
    - Backdrop: a `SphereGeometry(60, 32, 16)` with `side: BackSide`,
      vertex colors from void (top) to haze (at and below the horizon),
      `fog: false`, `depthWrite: false`, named `backdrop`.
    - Floor: `PlaneGeometry(80, 80)` rotated flat at `y = 0` with a
      `ShaderMaterial` (`transparent: true`, `depthWrite: false`) whose
      fragment shader draws anti-aliased grid lines every 1 unit using
      `fwidth`, colored cyan, faded by distance from the origin into the
      haze color, plus a dark glossy base. Uniforms: `uCyan`, `uHaze`,
      `uScroll` (grid scroll along z, advanced in `update` by
      `0.15 * dt`), `uReveal` (radius; lines beyond it are hidden; default
      `100`, set by `setReveal`). Named `floor`.
    - Fog: return nothing; `mount` (Task 7) sets
      `scene.fog = new THREE.FogExp2(colors.haze, 0.045)`.
    - Particles: `PARTICLE_COUNT` positions from `mulberry32(seed)` in a box
      `x, z in [-12, 12]`, `y in [0, 6]`, colors mixing cyan and violet,
      `PointsMaterial({ size: 0.035, vertexColors: true, transparent: true, opacity: 0.8, blending: AdditiveBlending, depthWrite: false })`.
      `update` lifts each particle by `0.05 * dt` and wraps at the top;
      mark the attribute `needsUpdate`.
    - Beams: `BEAM_COUNT` tall `PlaneGeometry(1.2, 9)` quads angled down
      from above toward the projector, additive cyan or violet with a
      vertical alpha gradient (vertex colors or a tiny shader). Each has a
      seeded period between 6 and 10 s and phase; `update` sets
      `opacity = 0.05 + 0.07 * (0.5 + 0.5 * sin(2 * PI * t / period + phase))`.
    - `mirror(source)`: `source.clone(true)` into a group named `mirror`,
      `scale.y = -1`, each cloned material cloned with `opacity *= 0.25`;
      `update` walks source and clone in parallel copying `rotation`,
      `scale`, `visible`, `position` (with `y` negated at the root) and
      material `color`.
    - `setEffects(f)`: `beams.visible = f.beams`;
      `particles.geometry.setDrawRange(0, Math.round(PARTICLE_COUNT * f.particleShare))`.
    - `dispose()`: dispose every geometry and material it created.
  - `camera.ts`: `HOME`, `WIDE`, `viewOffset` as the test pins it
    (`x = width / 2 - (focus.x + focus.width / 2)`, same for `y`;
    the caller passes it to `camera.setViewOffset(w, h, x, y, w, h)` and
    `clearViewOffset()` when it is `{0, 0}`), `parallax` with clamped
    pointer and the 2 degree maximum, zero unless motion is `full`.
- [ ] **Step 4: Run** `npx vitest run src/scene` (pass), the full check run.
  The world is not mounted yet, so the screenshot is unchanged; skip it.
- [ ] **Step 5: Commit** `feat(web): build the holodeck world for the scene`.

---

### Task 5: Projector, crystal core and context ring

**Files:**
- Create: `web/src/scene/core.ts`, `web/src/scene/core.test.ts`

**Interfaces:**
- Consumes: `SceneColors` (Task 4), `ContextLevel`, `CoreMode`,
  `contextLevel` (Task 2), `Motion` from `state/motion.ts`.
- Produces:
  - `interface CoreLook { color: 'cyan' | 'magenta'; spin: number; pulsePeriod: number; intensity: number; flicker: boolean }`
  - `coreLook(mode: CoreMode): CoreLook`
  - `interface Core { group: THREE.Group; emissive: THREE.Object3D; setMode(m: CoreMode): void; setFill(fill: number): void; flash(color: THREE.Color): void; setBoot(p: { rings: number; ignite: number }): void; update(dt: number, t: number, motion: Motion): boolean; dispose(): void }`
    where `update` returns true while a transition or flash is still
    animating (Task 7 uses it to choose 60 fps).
  - `createCore(colors: SceneColors): Core`

- [ ] **Step 1: Write the failing tests.**

```ts
import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { coreLook, createCore } from './core';
import { readColors } from './colors';

describe('coreLook', () => {
  it('breathes slowly when idle and spins faster while working', () => {
    expect(coreLook('idle')).toMatchObject({ color: 'cyan', pulsePeriod: 4, flicker: false });
    expect(coreLook('working').spin).toBeGreaterThan(coreLook('idle').spin);
    expect(coreLook('working').intensity).toBeGreaterThan(coreLook('idle').intensity);
  });

  it('turns magenta when it needs you and flickers on error', () => {
    expect(coreLook('attention')).toMatchObject({ color: 'magenta', flicker: false });
    expect(coreLook('error')).toMatchObject({ color: 'magenta', flicker: true });
  });
});

describe('createCore', () => {
  const ring = (c: ReturnType<typeof createCore>) => c.group.getObjectByName('context-ring') as THREE.Mesh;

  it('stacks three projector rings under a light cone', () => {
    const c = createCore(readColors());
    expect(c.group.getObjectByName('projector')!.children.filter((o) => o.name === 'pedestal-ring')).toHaveLength(3);
    expect(c.group.getObjectByName('cone')).toBeDefined();
  });

  it('colors the context ring by level and sizes it by fill', () => {
    const colors = readColors();
    const c = createCore(colors);
    c.setFill(0.5);
    const mat = () => ring(c).material as THREE.MeshBasicMaterial;
    expect(mat().color.equals(colors.cyan)).toBe(true);
    c.setFill(0.8);
    expect(mat().color.equals(colors.amber)).toBe(true);
    c.setFill(0.95);
    expect(mat().color.equals(colors.magenta)).toBe(true);
    const len = (ring(c).geometry as THREE.RingGeometry).parameters.thetaLength;
    expect(len).toBeCloseTo(Math.PI * 2 * 0.95);
  });

  it('snaps to the attention color at once outside full motion', () => {
    const colors = readColors();
    const c = createCore(colors);
    c.setMode('attention');
    c.update(0.016, 0, 'reduced');
    const heart = c.group.getObjectByName('heart') as THREE.Mesh;
    expect((heart.material as THREE.MeshBasicMaterial).color.equals(colors.magenta)).toBe(true);
  });

  it('reports that it is animating while a flash fades', () => {
    const c = createCore(readColors());
    c.flash(readColors().teal);
    expect(c.update(0.016, 0, 'reduced')).toBe(true);
    for (let i = 0; i < 100; i++) c.update(0.016, i * 0.016, 'reduced');
    expect(c.update(0.016, 2, 'reduced')).toBe(false);
  });
});
```

- [ ] **Step 2: Run** `npx vitest run src/scene/core.test.ts`; it fails.
- [ ] **Step 3: Implement** `core.ts`:
  - `projector` group: three `pedestal-ring` meshes (`TorusGeometry` radii
    1.1, 0.9, 0.7, tube 0.03, stacked at y 0.02, 0.12, 0.22), additive cyan;
    a `cone` (`CylinderGeometry(0.35, 1.0, 1.6, 48, 1, true)`) at y 0.9,
    additive cyan, opacity about 0.08, with a vertical fade.
  - Core at y 1.6: `crystal` (`OctahedronGeometry(0.55, 0)` scaled
    `(1, 1.4, 1)`, `MeshBasicMaterial` with low opacity fill) plus its
    `EdgesGeometry` as a bright `rim` (`LineSegments`), and an inner
    `heart` (`IcosahedronGeometry(0.18, 2)`, additive, full brightness). The
    three form `emissive`, which Task 7 mirrors under the floor.
  - `context-ring`: `RingGeometry(0.95, 1.02, 128, 1, PI / 2, max(0.0001, 2 * PI * fill))`
    at the core height, facing the camera (vertical); rebuilt only when the
    fill changes by more than 0.001; color by `contextLevel`.
  - `coreLook` values: idle `{ cyan, spin 0.15, pulsePeriod 4, intensity 1, flicker false }`,
    working `{ cyan, 0.9, 1.2, 1.6, false }`, attention `{ magenta, 0.3, 1.0, 1.4, false }`,
    error `{ magenta, 0.15, 0, 1.1, true }`.
  - `update(dt, t, motion)`: rotation by `spin * dt * motionScale` (see
    Task 7; here use 1 for full, 0.25 for reduced, 0 for off), breathing
    scale `1 + 0.06 * sin(2 * PI * t / pulsePeriod)` when the period is
    above 0, error flicker toggles `rim.visible` on a 90 ms cadence in full
    motion only, color steps with the existing `stepColor` semantics (move
    `stepColor` from `constellation.ts` into `core.ts` and keep its test
    in `core.test.ts`), a flash adds to the heart brightness and decays over
    600 ms. Returns true while color is still moving toward its target or a
    flash is alive.
  - `setBoot({ rings, ignite })`: shows the first `rings` pedestal rings
    (0 to 3) and scales the core opacity by `ignite` (0 to 1); default is
    fully on.
- [ ] **Step 4: Run** `npx vitest run src/scene` (pass), the full check run.
- [ ] **Step 5: Commit** `feat(web): add the projector and crystal core`.

---

### Task 6: Tool-call ring bursts

**Files:**
- Create: `web/src/scene/bursts.ts`, `web/src/scene/bursts.test.ts`

**Interfaces:**
- Consumes: `SceneColors`, `ToolCategory`.
- Produces:
  - `BURST_MS = 900`, `MAX_BURSTS = 12`
  - `categoryColor(colors: SceneColors, c: ToolCategory): THREE.Color`
  - `interface Bursts { group: THREE.Group; spawn(c: ToolCategory): void; update(dt: number): boolean; active(): number; dispose(): void }`
  - `createBursts(colors: SceneColors, origin: THREE.Vector3): Bursts`

- [ ] **Step 1: Write the failing tests.**

```ts
import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { BURST_MS, categoryColor, createBursts, MAX_BURSTS } from './bursts';
import { readColors } from './colors';

const colors = readColors();

describe('categoryColor', () => {
  it('maps read, write, run and network to cyan, teal, amber and violet', () => {
    expect(categoryColor(colors, 'read').equals(colors.cyan)).toBe(true);
    expect(categoryColor(colors, 'write').equals(colors.teal)).toBe(true);
    expect(categoryColor(colors, 'run').equals(colors.amber)).toBe(true);
    expect(categoryColor(colors, 'network').equals(colors.violet)).toBe(true);
  });
});

describe('createBursts', () => {
  it('expands a ring and removes it after the burst time', () => {
    const b = createBursts(colors, new THREE.Vector3(0, 1.6, 0));
    b.spawn('write');
    expect(b.active()).toBe(1);
    const ring = b.group.children.find((o) => o.visible) as THREE.Mesh;
    expect((ring.material as THREE.MeshBasicMaterial).color.equals(colors.teal)).toBe(true);
    const start = ring.scale.x;
    expect(b.update(0.3)).toBe(true);
    expect(ring.scale.x).toBeGreaterThan(start);
    b.update(BURST_MS / 1000);
    expect(b.active()).toBe(0);
    expect(b.update(0.016)).toBe(false);
  });

  it('recycles the oldest ring past the cap', () => {
    const b = createBursts(colors, new THREE.Vector3());
    for (let i = 0; i < MAX_BURSTS + 5; i++) b.spawn('read');
    expect(b.active()).toBe(MAX_BURSTS);
    expect(b.group.children).toHaveLength(MAX_BURSTS);
  });
});
```

- [ ] **Step 2: Run**; it fails.
- [ ] **Step 3: Implement** a pool of `MAX_BURSTS` thin flat rings
  (`RingGeometry(0.96, 1, 96)`, additive, `depthWrite: false`) created up
  front and hidden. `spawn` takes a free ring (or the oldest), sets color and
  age 0. `update(dt)` advances age; scale goes from 0.3 to 2.6 with expo-out
  over `BURST_MS`, opacity from 0.9 to 0; returns whether any ring is still
  visible.
- [ ] **Step 4: Run** `npx vitest run src/scene` (pass), the full check run.
- [ ] **Step 5: Commit** `feat(web): fire ring bursts colored by tool
  category`.

---

### Task 7: Mount the new scene (bloom, render loop, frame-time guard)

**Files:**
- Create: `web/src/scene/loop.ts`, `web/src/scene/loop.test.ts`
- Create: `web/src/scene/guard.ts`, `web/src/scene/guard.test.ts`
- Create: `web/src/scene/mount.ts`
- Delete: `web/src/scene/constellation.ts`, `web/src/scene/constellation.test.ts`
  (their remaining test moved in Task 5)
- Modify: `web/src/ui/stage/SceneLayer.tsx`, `web/src/state/store.ts`
  (`actions.effectsReduced`), tests that mock `scene/constellation`
- Test: `web/src/ui/stage/SceneLayer.test.tsx` (new)

**Interfaces:**
- Consumes: Tasks 2 to 6.
- Produces:
  - `motionScale(m: Motion): number` (full 1, reduced 0.25, off 0)
  - `frameInterval(o: { hidden: boolean; motion: Motion; eventActive: boolean }): number | null`
  - `MAX_STEP_S = 0.05`, `clampStep(dtMs: number): number` (seconds)
  - `GUARD = { budgetMs: 25, windowMs: 3000, cooldownMs: 10_000, maxGapMs: 250 }`
  - `class FrameGuard { constructor(level?: EffectsLevel); sample(dtMs: number, now: number): EffectsLevel | null; reset(level: EffectsLevel, now: number): void; get level(): EffectsLevel }`
  - `interface SceneInput { mode: CoreMode; fill: number; motion: Motion; effects: EffectFlags; booting: boolean; focus: { x: number; y: number; width: number; height: number } | null }`
  - `interface SceneHandle { update(s: SceneInput): void; burst(c: ToolCategory): void; dispose(): void; camera: THREE.PerspectiveCamera; world: THREE.Scene; requestFrame(): void; setCameraPose(p: { position: THREE.Vector3; target: THREE.Vector3 }): void; canvas: HTMLCanvasElement }`
  - `mountScene(canvas: HTMLCanvasElement, initial: SceneInput, hooks: { onStepDown(level: EffectsLevel): void; onFail(): void }): SceneHandle`
  - `actions.effectsReduced(level: EffectsLevel)`: sets `effectsLevel` and
    pushes the warn toast with `action: 'restore-effects'`.

- [ ] **Step 1: Write the failing tests.**

`loop.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { clampStep, frameInterval, MAX_STEP_S, motionScale } from './loop';

describe('frameInterval', () => {
  it('runs at 60 fps for events, 30 fps for ambient motion, and stops when hidden or off', () => {
    expect(frameInterval({ hidden: false, motion: 'full', eventActive: true })).toBeCloseTo(1000 / 60);
    expect(frameInterval({ hidden: false, motion: 'full', eventActive: false })).toBeCloseTo(1000 / 30);
    expect(frameInterval({ hidden: false, motion: 'reduced', eventActive: false })).toBeCloseTo(1000 / 30);
    expect(frameInterval({ hidden: true, motion: 'full', eventActive: true })).toBeNull();
    expect(frameInterval({ hidden: false, motion: 'off', eventActive: true })).toBeNull();
  });
});

describe('motionScale', () => {
  it('runs ambient motion at a quarter speed when reduced', () => {
    expect(motionScale('full')).toBe(1);
    expect(motionScale('reduced')).toBe(0.25);
    expect(motionScale('off')).toBe(0);
  });
});

describe('clampStep', () => {
  it('clamps the step after a long gap', () => {
    expect(clampStep(16)).toBeCloseTo(0.016);
    expect(clampStep(120_000)).toBe(MAX_STEP_S);
    expect(clampStep(-5)).toBe(0);
  });
});
```

`guard.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { FrameGuard, GUARD } from './guard';

function feed(g: FrameGuard, dtMs: number, fromMs: number, untilMs: number) {
  const changes: number[] = [];
  for (let now = fromMs; now <= untilMs; now += dtMs) {
    const r = g.sample(dtMs, now);
    if (r !== null) changes.push(r);
  }
  return changes;
}

describe('FrameGuard', () => {
  it('keeps everything on while frames are fast', () => {
    expect(feed(new FrameGuard(), 16, 0, 20_000)).toEqual([]);
  });

  it('steps down once per cooldown while frames stay slow', () => {
    const g = new FrameGuard();
    expect(feed(g, 40, 0, GUARD.windowMs + 40)).toEqual([1]);
    // Still slow, but inside the 10 s cooldown: no further step.
    expect(feed(g, 40, GUARD.windowMs + 80, GUARD.windowMs + 9_000)).toEqual([]);
    expect(feed(g, 40, GUARD.windowMs + 9_040, 40_000)).toEqual([2, 3]);
    expect(g.level).toBe(3);
  });

  it('never steps below the last level', () => {
    const g = new FrameGuard(3);
    expect(feed(g, 40, 0, 60_000)).toEqual([]);
  });

  it('ignores gaps such as a hidden tab', () => {
    const g = new FrameGuard();
    feed(g, 16, 0, 1000);
    expect(g.sample(120_000, 121_000)).toBeNull();
    expect(feed(g, 16, 121_016, 130_000)).toEqual([]);
  });

  it('waits a cooldown after a restore', () => {
    const g = new FrameGuard(2);
    g.reset(0, 50_000);
    expect(feed(g, 40, 50_040, 59_000)).toEqual([]);
    expect(feed(g, 40, 59_040, 64_000)).toEqual([1]);
  });
});
```

`SceneLayer.test.tsx`:

```ts
import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { sceneFailed } from '../../state/effects';

const hooks: { onFail?: () => void } = {};
vi.mock('../../scene/mount', () => ({
  mountScene: (_c: HTMLCanvasElement, _s: unknown, h: { onFail(): void }) => {
    hooks.onFail = h.onFail;
    return { update() {}, burst() {}, dispose() {}, requestFrame() {}, setCameraPose() {} };
  },
}));

describe('SceneLayer', () => {
  it('falls back on context loss', async () => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    const { SceneLayer } = await import('./SceneLayer');
    const root = document.createElement('div');
    try {
      render(<SceneLayer />, root);
      await vi.waitFor(() => expect(hooks.onFail).toBeDefined());
      hooks.onFail!();
      expect(sceneFailed.value).toBe(true);
    } finally {
      render(null, root);
      sceneFailed.value = false;
      HTMLCanvasElement.prototype.getContext = original;
    }
  });
});
```

- [ ] **Step 2: Run**; they fail.
- [ ] **Step 3: Implement.**
  - `loop.ts`: as tested. `clampStep(dtMs) = min(MAX_STEP_S, max(0, dtMs / 1000))`.
  - `guard.ts`: keep `sum`, `count`, `windowStart`, `lastChange = -Infinity`.
    `sample`: if `dtMs > maxGapMs`, restart the window at `now` and return
    null. Otherwise accumulate; when `now - windowStart >= windowMs`,
    compute the average, restart the window, and if
    `avg > budgetMs && level < 3 && now - lastChange >= cooldownMs` raise the
    level, set `lastChange = now` and return it. `reset(level, now)` sets
    the level, `lastChange = now`, and restarts the window.
  - `mount.ts`:
    - `WebGLRenderer({ canvas, antialias: true, alpha: false, powerPreference: 'high-performance' })`,
      pixel ratio `min(devicePixelRatio, 2)`, `toneMapping: ACESFilmicToneMapping`.
    - Scene with `createWorld`, `createCore`, `createBursts(colors, core position)`,
      `world.mirror(core.emissive)`, fog `FogExp2(colors.haze, 0.045)`.
    - Camera at `HOME`; on `update`, apply `viewOffset` from `focus` and
      the pointer `parallax` (pointer listener on `window`, values eased).
    - Composer: `EffectComposer` with `RenderPass`, `UnrealBloomPass`
      (strength 1.1, radius 0.6, threshold 0.15) and `OutputPass`, imported
      from `three/addons/postprocessing/*.js`. Half resolution: override the
      bloom pass `setSize` to call the original with `w / 2, h / 2`. When
      `effects.bloom` is false render with `renderer.render(scene, camera)`
      instead of the composer.
    - Loop: one `requestAnimationFrame` chain. Each frame computes
      `dt = clampStep(now - last)`, feeds `guard.sample(now - last, now)`
      only while `effects.bloom || effects.beams || effects.particleShare > 0.5`
      can still step down, updates world, core and bursts with
      `dt * motionScale(motion)` (bursts and core flashes use real `dt`),
      renders, then schedules the next frame only if
      `frameInterval({ hidden: document.hidden, motion, eventActive })` is
      not null, where `eventActive` is any of: bursts alive, core
      animating, mode `working`, camera pose easing, boot running. For a
      30 fps interval skip frames whose time since the last render is below
      the interval. `requestFrame()` (and every `update`, `burst` and
      `visibilitychange`) kicks the chain if it stopped. Motion `off`
      renders exactly one frame per kick.
    - `burst(c)`: in full motion `bursts.spawn(c)`, in reduced
      `core.flash(categoryColor(colors, c))`, in off nothing but a frame.
    - Guard step-down: `hooks.onStepDown(level)`; the caller turns it into
      `effectsLevel` and the scene gets the new flags through `update`.
    - `canvas.addEventListener('webglcontextlost', (e) => { e.preventDefault(); hooks.onFail(); })`.
    - `dispose()` as the old module did, plus composer and listeners.
  - `SceneLayer.tsx`: dynamic `import('../../scene/mount')`; builds
    `SceneInput` from `coreMode(state)`, `contextFill`, `motion`,
    `effectFlags(effectsLevel, lowPower)`, `booting` (`conn === 'booting'`)
    and `stageRect`; calls `update` on change. A `useEffect` on
    `telemetry.value.tools` remembers the last id it burst and calls
    `scene.burst(e.category)` for each newer event. `onStepDown` calls
    `actions.effectsReduced(level)`; `onFail` sets `sceneFailed.value = true`.
    `restoreEffects()` must also reach the guard: `SceneInput.effects`
    returning to level 0 calls `guard.reset(0, performance.now())` inside
    `mount.ts`.
- [ ] **Step 4: Run** `npx vitest run` (pass), the full check run, e2e (6
  passed). Check the build output: three.js and postprocessing must be in a
  chunk separate from the entry chunk (`ls -la internal/web/dist/assets`).
- [ ] **Step 5: Screenshot, look at it, commit** `feat(web): mount the
  neon scene with bloom and a frame-time guard`.

---

### Task 8: HUD instruments

**Files:**
- Create: `web/src/ui/hud/ContextGauge.tsx`, `web/src/ui/hud/Numeral.tsx`,
  `web/src/ui/hud/Sparkline.tsx`, `web/src/ui/hud/ActivityChart.tsx`,
  `web/src/ui/hud/ServerHexes.tsx`, `web/src/ui/hud/series.ts`
- Create: `web/src/ui/hud/series.test.ts`, `web/src/ui/hud/instruments.test.tsx`
- Create: `web/src/styles/hud.css` (imported in `main.tsx` after `shell.css`)
- Modify: `web/src/ui/Hud.tsx`, `web/src/ui/Hud.test.tsx`,
  `web/src/styles/shell.css` (HUD grid only)

**Interfaces:**
- Consumes: `telemetry` (Task 2), `contextLevel`, `contextFill`,
  `ToolCategory`.
- Produces:
  - `sparkPoints(values: number[], width: number, height: number): string`
  - `activityBars(events: ToolEvent[], now: number, minutes?: number): Record<ToolCategory, number>[]`
    (index 0 is the oldest minute, the last index the current minute)
  - `serverTone(kind: 'lsp' | 'mcp', state: string): 'lit' | 'dim' | 'error'`
  - `digits(text: string): string[]`
  - Components: `ContextGauge({ fill })`, `Numeral({ text, label })`,
    `Sparkline({ values, label })`, `ActivityChart({ events, now })`,
    `ServerHexes({ kind, servers })`.

- [ ] **Step 1: Write the failing tests.**

`series.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { activityBars, digits, serverTone, sparkPoints } from './series';

describe('sparkPoints', () => {
  it('draws a flat midline for fewer than two values', () => {
    expect(sparkPoints([], 60, 16)).toBe('0,8 60,8');
    expect(sparkPoints([5], 60, 16)).toBe('0,8 60,8');
  });

  it('scales values into the box with the top at the maximum', () => {
    expect(sparkPoints([0, 5, 10], 60, 16)).toBe('0,16 30,8 60,0');
  });

  it('draws a flat line when all values are equal', () => {
    expect(sparkPoints([3, 3, 3], 60, 16)).toBe('0,8 30,8 60,8');
  });
});

describe('activityBars', () => {
  const at = (minAgo: number) => 15 * 60_000 - minAgo * 60_000 - 1;
  it('buckets events per minute by category, oldest first', () => {
    const bars = activityBars([
      { id: 'a', category: 'read', at: at(0) },
      { id: 'b', category: 'read', at: at(0) },
      { id: 'c', category: 'write', at: at(2) },
      { id: 'd', category: 'run', at: at(14) },
      { id: 'e', category: 'network', at: at(15) },
    ], 15 * 60_000);
    expect(bars).toHaveLength(15);
    expect(bars[14]).toEqual({ read: 2, write: 0, run: 0, network: 0 });
    expect(bars[12]).toEqual({ read: 0, write: 1, run: 0, network: 0 });
    expect(bars[0]).toEqual({ read: 0, write: 0, run: 1, network: 0 });
  });
});

describe('serverTone', () => {
  it('lights ready LSPs and connected MCPs and flags errors', () => {
    expect(serverTone('lsp', '2')).toBe('lit');
    expect(serverTone('lsp', '3')).toBe('error');
    expect(serverTone('lsp', '1')).toBe('dim');
    expect(serverTone('mcp', 'connected')).toBe('lit');
    expect(serverTone('mcp', 'error')).toBe('error');
    expect(serverTone('mcp', 'needs auth')).toBe('error');
    expect(serverTone('mcp', 'disabled')).toBe('dim');
  });
});

describe('digits', () => {
  it('splits a readout into characters', () => {
    expect(digits('$0.12')).toEqual(['$', '0', '.', '1', '2']);
  });
});
```

`instruments.test.tsx`:

```ts
import { describe, expect, it } from 'vitest';
import { render } from 'preact';
import { ContextGauge } from './ContextGauge';
import { Numeral } from './Numeral';

const mount = (vnode: preact.VNode) => {
  const root = document.createElement('div');
  render(vnode, root);
  return root;
};

describe('ContextGauge', () => {
  it('speaks its value and carries the level', () => {
    const root = mount(<ContextGauge fill={0.42} />);
    const meter = root.querySelector('[role="meter"]')!;
    expect(meter.getAttribute('aria-label')).toBe('Context');
    expect(meter.getAttribute('aria-valuenow')).toBe('42');
    expect(meter.getAttribute('aria-valuetext')).toBe('42 percent');
    expect(meter.getAttribute('data-level')).toBe('ok');
  });

  it('clamps the gauge', () => {
    for (const [fill, want, level] of [[Number.NaN, '0', 'ok'], [-1, '0', 'ok'], [3, '100', 'critical']] as const) {
      const meter = mount(<ContextGauge fill={fill} />).querySelector('[role="meter"]')!;
      expect(meter.getAttribute('aria-valuenow')).toBe(want);
      expect(meter.getAttribute('data-level')).toBe(level);
    }
  });
});

describe('Numeral', () => {
  it('rolls each digit with a data attribute and keeps the text readable', () => {
    const root = mount(<Numeral text="1.5k" label="Tokens" />);
    expect(root.querySelector('.visually-hidden')!.textContent).toBe('Tokens 1.5k');
    expect(Array.from(root.querySelectorAll('.roll')).map((e) => e.getAttribute('data-d'))).toEqual(['1', '5']);
    expect(root.innerHTML).not.toContain('style=');
  });
});
```

  `Hud.test.tsx`: keep the input mode tests; add one asserting the HUD shows
  the gauge, the two numerals, the activity chart
  (`role="img"` named `Tool calls in the last 15 minutes: 0`), and one hex
  tile per LSP and MCP server in `state.value.lsp` and `state.value.mcp`,
  each with a `<title>` holding `name: tone`.
- [ ] **Step 2: Run**; they fail.
- [ ] **Step 3: Implement.**
  - `series.ts`: as tested. `sparkPoints` maps index `i` to
    `x = round(i * width / (n - 1))` and value to
    `y = round(height - (v - min) / (max - min) * height)` (midline when
    `max === min`). `activityBars` bucket is
    `minutes - 1 - floor((now - at) / 60000)`, dropping buckets outside
    `[0, minutes)`. `serverTone`: lsp `'2'` lit, `'3'` error, else dim; mcp
    `connected` lit, `error` or `needs auth` error, else dim.
  - `ContextGauge`: 56 px SVG, background circle plus a value circle with
    `stroke-dasharray={`${c * f} ${c}`}` (an attribute, CSP-safe), where
    `f` is the clamped fill (`Number.isFinite(fill) ? min(1, max(0, fill)) : 0`);
    percent text in the center; the wrapper has
    `role="meter" aria-label="Context" aria-valuemin="0" aria-valuemax="100"`
    and `aria-valuenow`, `aria-valuetext`, `data-level`. CSS colors the
    value stroke cyan, amber or magenta by `data-level`; `critical` pulses
    (no pulse under reduced or off motion).
  - `Numeral`: visually hidden `label text`; an `aria-hidden` row where each
    digit is `<span class="roll" data-d="7"><span class="roll__strip">0..9 as ten spans</span></span>`
    and other characters are plain spans. `hud.css` has
    `.roll[data-d='N'] .roll__strip { transform: translateY(-N0%); }` for N
    0 to 9 with a 400 ms transform transition; numerals use Chakra Petch,
    `font-variant-numeric: tabular-nums`, `text-shadow: var(--glow-text)`.
  - `Sparkline`: 60 by 16 SVG polyline from `sparkPoints`, `aria-hidden`.
    Tokens use `telemetry.value.usage.map((u) => u.tokens)`, cost uses
    `.cost`.
  - `ActivityChart`: 15 bars, each a stack of up to four rects in the
    category colors (classes `bar--read` and so on), scaled to the largest
    total (at least 1). Each bar has a `<title>` with its counts. The SVG
    has `role="img"` and `aria-label="Tool calls in the last 15 minutes: N"`.
    `now` comes from a 15 s `setInterval` tick in `Hud` plus the latest
    telemetry change, so the current minute grows live.
  - `ServerHexes`: one small SVG hexagon per server with `data-tone`,
    `<title>{name}: {tone}</title>`, and an error glyph (an `!` text) when
    the tone is error. Lit is cyan, dim is muted at 40 % opacity, error is
    magenta.
  - `Hud.tsx` layout per spec 3.3: identity block (project, title, model
    with a small provider glyph, branch), the gauge, tokens and cost
    numerals with sparklines, the activity chart, the LSP and MCP hexes,
    then the existing controls. Below 1024 px hide the chart and sparklines
    (`hud.css` media query); numbers stay.
- [ ] **Step 4: Run** `npx vitest run` (pass), the full check run, e2e.
- [ ] **Step 5: Screenshot, look at it, commit** `feat(web): turn the HUD
  into instruments`.

---

### Task 9: Boot sequence in the scene

**Files:**
- Create: `web/src/scene/boot.ts`, `web/src/scene/boot.test.ts`
- Modify: `web/src/scene/mount.ts`, `web/src/ui/BootSequence.tsx`,
  `web/src/styles/shell.css` (boot rules)
- Test: a new `web/src/ui/BootSequence.test.tsx`

**Interfaces:**
- Consumes: `World.setReveal`, `Core.setBoot` (Tasks 4 and 5).
- Produces: `BOOT_MS = 1400`,
  `bootFrame(elapsedMs: number, motion: Motion): { reveal: number; rings: number; ignite: number; done: boolean }`.

- [ ] **Step 1: Write the failing tests.**

```ts
import { describe, expect, it } from 'vitest';
import { BOOT_MS, bootFrame } from './boot';

describe('bootFrame', () => {
  it('draws the grid outward, then stacks the rings, then ignites the core', () => {
    const start = bootFrame(0, 'full');
    expect(start).toEqual({ reveal: 0, rings: 0, ignite: 0, done: false });
    const mid = bootFrame(BOOT_MS * 0.5, 'full');
    expect(mid.reveal).toBeGreaterThan(0);
    expect(mid.rings).toBeGreaterThan(0);
    expect(mid.ignite).toBe(0);
    expect(bootFrame(BOOT_MS, 'full')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
  });

  it('skips straight to the end outside full motion', () => {
    expect(bootFrame(0, 'reduced')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
    expect(bootFrame(0, 'off')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
  });
});
```

  `BootSequence.test.tsx`: while booting it renders a hologram status
  `Linking to the crush session` with `role="status"`; the overlay no longer
  paints an opaque background (assert the element has class `holo` and not
  the old `boot__rings` child).
- [ ] **Step 2: Run**; they fail.
- [ ] **Step 3: Implement.** Phases: grid reveal over 0 to 45 % of
  `BOOT_MS` (radius 0 to 40, expo-out), rings appear one by one over 35 to
  70 %, core ignites over 70 to 100 %. `mount.ts` runs it from the first
  frame and holds the last phase until `booting` turns false, then lets it
  finish. `BootSequence` becomes a centered hologram label (no opaque
  backdrop, no CSS rings) that fades out once linked; when WebGL is off it
  keeps the old CSS rings so the 2D view still has a loading state.
- [ ] **Step 4: Run** `npx vitest run` (pass), the full check run, e2e.
- [ ] **Step 5: Screenshot (take one at 600 ms as well), commit**
  `feat(web): power up the scene while the session links`.

---

### Task 10: Cinematic mode

**Files:**
- Create: `web/src/state/cinematic.ts`, `web/src/state/cinematic.test.ts`
- Create: `web/src/scene/cssProjection.ts`, `web/src/scene/cssProjection.test.ts`
- Create: `web/src/scene/cinematic.ts` (lazy chunk)
- Create: `web/src/styles/cinematic.css`
- Modify: `web/src/ui/App.tsx`, `web/src/ui/Hud.tsx`,
  `web/src/ui/stage/SceneLayer.tsx`, `web/src/ui/deck/CommandDeck.tsx`
  (slim class only), `web/src/main.tsx` (css import)
- Modify: the spec, section 3.7, "Transition" bullet (see Step 6)

**Interfaces:**
- Consumes: `SceneHandle` (Task 7), `effectFlags(...).cinematic`.
- Produces:
  - `cinematic: Signal<boolean>`, `setCinematic(on: boolean): void`
  - `type KeyContext = { code: string; altKey: boolean; key: string; inPrompt: boolean; dialogOpen: boolean; active: boolean; allowed: boolean }`
  - `cinematicKey(k: KeyContext): 'toggle' | 'exit' | null`
  - `FLIGHT_MS = 900`, `FADE_MS = 150`
  - `cameraCSS(camera: THREE.PerspectiveCamera, width: number, height: number): { perspective: string; transform: string }`
  - `objectCSS(object: THREE.Object3D): string`
  - `startCinematic(scene: SceneHandle, els: { view: HTMLElement; camera: HTMLElement; panels: HTMLElement[] }, motion: Motion): { stop(): Promise<void> }`

- [ ] **Step 1: Write the failing tests.**

`cinematic.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { cinematicKey, type KeyContext } from './cinematic';

const k = (over: Partial<KeyContext>): KeyContext => ({
  code: 'KeyC', altKey: true, key: 'ç', inPrompt: false, dialogOpen: false, active: false, allowed: true, ...over,
});

describe('cinematicKey', () => {
  it('toggles on Alt+C by physical key, including while typing', () => {
    expect(cinematicKey(k({}))).toBe('toggle');
    expect(cinematicKey(k({ inPrompt: true }))).toBe('toggle');
    expect(cinematicKey(k({ active: true }))).toBe('toggle');
  });

  it('ignores C without Alt and other keys with Alt', () => {
    expect(cinematicKey(k({ altKey: false, key: 'c' }))).toBeNull();
    expect(cinematicKey(k({ code: 'KeyV' }))).toBeNull();
  });

  it('does nothing when cinematic mode is unavailable', () => {
    expect(cinematicKey(k({ allowed: false }))).toBeNull();
  });

  it('exits on Esc only outside the prompt and with no dialog open', () => {
    const esc = { code: 'Escape', key: 'Escape', altKey: false, active: true };
    expect(cinematicKey(k(esc))).toBe('exit');
    expect(cinematicKey(k({ ...esc, inPrompt: true }))).toBeNull();
    expect(cinematicKey(k({ ...esc, dialogOpen: true }))).toBeNull();
    expect(cinematicKey(k({ ...esc, active: false }))).toBeNull();
  });
});
```

`cssProjection.test.ts` (the expected strings come from three's own
`CSS3DRenderer`; the test renders a `CSS3DObject` with it in jsdom and
compares, so the port cannot drift):

```ts
import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { CSS3DObject, CSS3DRenderer } from 'three/addons/renderers/CSS3DRenderer.js';
import { cameraCSS, objectCSS } from './cssProjection';

describe('css projection', () => {
  it('matches CSS3DRenderer for the camera and an object', () => {
    const camera = new THREE.PerspectiveCamera(42, 1440 / 860, 0.1, 100);
    camera.position.set(0, 4.6, 11.5);
    camera.lookAt(0, 1.2, 0);
    camera.updateMatrixWorld();
    const scene = new THREE.Scene();
    const el = document.createElement('div');
    const obj = new CSS3DObject(el);
    obj.position.set(-3, 1.5, 0);
    obj.rotation.y = 0.6;
    obj.scale.setScalar(0.004);
    scene.add(obj);
    const r = new CSS3DRenderer();
    r.setSize(1440, 860);
    r.render(scene, camera);
    const cameraEl = r.domElement.firstElementChild as HTMLElement;
    const css = cameraCSS(camera, 1440, 860);
    expect(css.transform).toBe(cameraEl.style.transform);
    expect(css.perspective).toBe(r.domElement.style.perspective);
    expect(objectCSS(obj)).toBe(el.style.transform);
  });
});
```

  If jsdom normalizes the `style.transform` strings differently from what
  the port produces, compare the parsed numbers instead of the raw strings,
  but keep the comparison against `CSS3DRenderer` output.

- [ ] **Step 2: Run**; they fail.
- [ ] **Step 3: Implement.**
  - `state/cinematic.ts`: `cinematic` signal (not persisted), `cinematicKey`
    as tested (`code === 'KeyC' && altKey` gives toggle when allowed; Esc
    gives exit when `active && !inPrompt && !dialogOpen`). `App.tsx`
    installs one `keydown` listener on `window` (capture phase) that builds
    the context (`inPrompt`: `document.activeElement` is the deck textarea;
    `dialogOpen`: `needsAttention(state)`; `allowed`:
    `use3D() && effectFlags(...).cinematic`), calls `preventDefault()` for
    a non-null result, and flips the signal. Leaving low power off or a
    scene failure while active sets it false.
  - HUD button `Cinematic` with `aria-pressed`, `title="Cinematic mode (Alt+C)"`,
    hidden when not allowed.
  - `cssProjection.ts`: a direct port of the camera and object matrix
    functions in `three/examples/jsm/renderers/CSS3DRenderer.js`
    (`epsilon`, `getCameraCSSMatrix`, `getObjectCSSMatrix`, and the
    `fov`, `perspective` and `translate` composition in `render`) with a
    comment naming the source file and its MIT license.
  - Why a port instead of the renderer itself: `CSS3DRenderer` appends each
    object's element to its own camera element, which would move the log
    out of its place in the DOM, break Preact's ownership of that node, and
    change the reading order. The port applies the same matrices to two
    wrapper elements Preact renders around the log
    (`<div class="cine-view"><div class="cine-camera"><section class="panel panel--log ...">`).
    The wrappers are `display: contents` outside cinematic mode, so the
    normal layout is unchanged and the DOM order never changes.
  - `scene/cinematic.ts` (dynamic import): on start, eases the camera from
    `HOME` to `WIDE` over `FLIGHT_MS` (expo-out) through
    `scene.setCameraPose`, or jumps and cross-fades the panels over
    `FADE_MS` when motion is not full. Creates a `THREE.Object3D` anchor for
    the log at `(-3.2, 1.8, 0.6)` facing the projector
    (`rotation.y = 0.55`) with `scale` 0.004 per CSS pixel. Each frame sets
    `view.style.perspective`, `camera.style.transform` and the panel's
    `style.transform` from the port (CSSOM only). Adds `OrbitControls`
    (`three/addons/controls/OrbitControls.js`) on the canvas with
    `minPolarAngle 0.35`, `maxPolarAngle 1.35` (camera stays above the
    floor), `minDistance 6`, `maxDistance 18`, `enablePan false`, target
    `WIDE.target`. `stop()` disposes the controls, flies back to `HOME`,
    clears every inline transform it set, and resolves when done.
  - `cinematic.css`: `[data-cinematic='true']` on `.holodeck` hides the
    HUD instruments except the cinematic button and link status, gives the
    log wrapper `position: fixed; inset: 0; overflow: hidden;
    pointer-events: none` (the log itself `pointer-events: auto`, fixed
    width 34rem, height 70vh), makes the deck the slim bar (single-line
    textarea, no help text), and keeps `.interrupt-layer` flat and on top.
    The canvas takes pointer events in this mode so drags orbit.
- [ ] **Step 4: Run** `npx vitest run` (pass), the full check run, e2e.
  Add one e2e case to `web/e2e/smoke.spec.ts`:

```ts
test('Alt+C enters and Esc leaves cinematic mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  const prompt = page.getByLabel('Transmit to crush');
  await prompt.focus();
  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await expect(prompt).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await page.locator('.hud__title').click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'false');
});
```

  If the e2e browser has no WebGL (cinematic hidden), the fixture launch
  arguments from Task 11 Step 1 are needed first; in that case land the
  launch arguments in this task.
- [ ] **Step 5: Screenshot in cinematic mode, look at it, commit**
  `feat(web): add cinematic mode`.
- [ ] **Step 6: Spec note.** In spec 3.7, replace "moves onto curved screens
  around the projector through three.js `CSS3DRenderer`" with "moves onto
  angled screens around the projector, placed with `CSS3DRenderer`'s
  projection math applied to wrapper elements in place (the renderer itself
  would move the panels in the DOM)". Commit it with the task.

---

### Task 11: Screenshot, axe and budget checks

**Files:**
- Modify: `web/package.json`, `web/package-lock.json`
  (`npm install -D @axe-core/playwright`)
- Modify: `web/playwright.config.ts`
- Create: `web/e2e/visual.spec.ts` and its committed baselines under
  `web/e2e/visual.spec.ts-snapshots/`
- Create: `web/scripts/budget.mjs`; add `"budget": "node scripts/budget.mjs"`
  to `package.json` scripts

**Interfaces:**
- Consumes: everything above; the `crush-web-seed` localStorage key (Task 4).

- [ ] **Step 1: Playwright config.** Give the chromium project
  `launchOptions: { args: ['--use-gl=swiftshader', '--enable-webgl', '--ignore-gpu-blocklist'] }`,
  `viewport: { width: 1440, height: 860 }`, and
  `expect: { toHaveScreenshot: { maxDiffPixelRatio: 0.02, animations: 'disabled' } }`.
- [ ] **Step 2: Write the visual tests.**

```ts
import AxeBuilder from '@axe-core/playwright';
import { test, expect } from './fixture';

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('crush-web-seed', '1234'));
  await page.clock.install({ time: new Date('2026-11-22T12:00:00Z') });
});

async function settle(page: import('@playwright/test').Page) {
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await page.clock.runFor(3000);
}

async function axe(page: import('@playwright/test').Page) {
  const { violations } = await new AxeBuilder({ page }).analyze();
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([]);
}

test('normal layout', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await expect(page).toHaveScreenshot('normal.png');
  await axe(page);
});

test('cinematic mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.keyboard.press('Alt+KeyC');
  await page.clock.runFor(1200);
  await expect(page).toHaveScreenshot('cinematic.png');
  await axe(page);
});

test('permission request', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByLabel('Transmit to crush').fill('run permission');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('alertdialog')).toBeVisible();
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('permission.png');
  await axe(page);
  await page.keyboard.press('d');
});

test('reduced motion', async ({ page, gateway }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(gateway.launch);
  await settle(page);
  await expect(page).toHaveScreenshot('reduced.png');
  await axe(page);
});
```

  webfake shares one session across tests in a worker, so the permission
  test answers its request at the end; if earlier tests leave messages that
  make the screenshots order-dependent, give each visual test its own
  webfake by using a test-scoped variant of the `gateway` fixture.
  The session title and token counts in the HUD come from webfake and are
  stable. If `page.clock` does not drive `requestAnimationFrame` in this
  Playwright version, read the clock API docs in
  `web/node_modules/playwright-core` and use what it offers; do not add
  sleeps to stabilize screenshots.
- [ ] **Step 3: Run once to create baselines**:
  `cd web && GOCACHE=/tmp/crush-go-build LD_LIBRARY_PATH=/tmp/alsa-stub npx --no-update-notifier playwright test visual --update-snapshots`.
  Open each baseline with the view tool and check it against spec section 3
  (grid floor, fog, projector, core, glass panels with brackets, HUD
  instruments). Then run the full suite three times without
  `--update-snapshots`; all must pass each time. Fix axe violations in the
  components (contrast, names, roles), never by excluding rules.
- [ ] **Step 4: Budget script.** `scripts/budget.mjs` reads
  `../internal/web/dist/index.html`, collects the entry script and every
  `modulepreload` and stylesheet it references, gzips each with
  `node:zlib` `gzipSync`, prints the total, and exits 1 above 600 * 1024
  bytes. Run `npm run budget`; it must pass and must not count the lazy
  three.js, cinematic or OrbitControls chunks.
- [ ] **Step 5: Run** the full check run, the whole e2e suite, and
  `npm run budget`. Commit `test(web): add screenshot, axe and size checks`.

---

## Screenshot (visual tasks)

Build webfake once per task, then take a 1440 by 860 JPEG of the running UI:

```bash
cd /home/pbtrudel/git/.crush-worktrees/web-mode && GOCACHE=/tmp/crush-go-build go build -o /tmp/stockcake-script/webfake ./internal/web/cmd/webfake
LD_LIBRARY_PATH=/tmp/alsa-stub timeout 90 node /tmp/stockcake-script/cur.cjs /home/pbtrudel/git/.crush-worktrees/web-mode/web/node_modules/playwright /tmp/stockcake-script/webfake /tmp/stockcake-shot /home/pbtrudel/git/.crush-worktrees/web-mode
```

The image is `/tmp/stockcake-shot/current.jpg`. Report what it shows in the
task report (two or three sentences) instead of attaching it.

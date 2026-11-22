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

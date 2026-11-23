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

  it('a snapshot after live calls records nothing new and keeps old ids seen', () => {
    const one = { ...live, messages: [asst('a', ['t1', 'view'])] };
    let t = observe(emptyTelemetry(), live, one, false, 1000);
    expect(t.tools.map((e) => e.id)).toEqual(['t1']);
    const two = { ...live, messages: [asst('a', ['t1', 'view'], ['t2', 'bash'])] };
    t = observe(t, one, two, true, 2000);
    expect(t.tools.map((e) => e.id)).toEqual(['t1']);
    expect(t.seen.has('t2')).toBe(true);
  });

  it('starts fresh when the session changes', () => {
    const a = { ...live, messages: [asst('a', ['t1', 'view'])], session: session(100, 0.01) };
    let t = observe(emptyTelemetry(), live, a, false, 1000);
    const other = { ...live, sessionId: 'x', messages: [] };
    t = observe(t, a, other, false, 2000);
    expect(t).toEqual(emptyTelemetry());
  });
});

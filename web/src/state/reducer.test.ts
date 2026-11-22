import { describe, expect, it } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame, Snapshot } from '../api/types';
import {
  adoptSession, contextFill, initialState, markAnswered, needsAttention, reduce, setConn, toolResults,
} from './reducer';

const fx = fixtures as unknown as Record<string, Frame>;
const snapshot = (over: Partial<Snapshot> = {}): Frame => ({
  seq: 10,
  type: 'snapshot',
  payload: {
    version: 'v1', working_dir: '/repo/holodeck', current_session_id: 's1',
    messages: [], permissions: [], permission_mode: 'normal', input_mode: 'code',
    model: { id: 'm', name: 'Model', provider: 'p', context_window: 200000 },
    busy: false, queued: [], ...over,
  },
});
const live = () => reduce(initialState(), snapshot());

describe('wire contract', () => {
  it('fixtures carry the fields the UI reads', () => {
    const msg = (fx.message_assistant.payload as any).payload;
    expect(msg.parts.map((p: any) => p.type)).toEqual(['reasoning', 'text', 'tool_call', 'finish']);
    expect(msg.parts[2].data).toMatchObject({ id: 'tc-1', name: 'view' });
    const q = (fx.question_batch_request.payload as any).payload;
    expect(q.questions[0]).toMatchObject({ id: 'flavor', type: 'single_choice', question: 'Which theme?' });
    expect((fx.permission_mode_changed.payload as any).payload.mode).toBe('yolo');
    expect((fx.run_complete.payload as any).payload.session_id).toBe('s1');
    expect((fx.permission_request.payload as any).payload).toMatchObject({ id: 'p-1', tool_call_id: 'tc-2' });
    expect((fx.session.payload as any).payload).toMatchObject({ id: 's1', prompt_tokens: 52000 });
  });
});

describe('reduce', () => {
  it('snapshot sets live state and seq', () => {
    const s = live();
    expect(s.conn).toBe('live');
    expect(s.lastSeq).toBe(10);
    expect(s.sessionId).toBe('s1');
    expect(s.project).toBe('holodeck');
  });

  it('upserts messages by id in created order', () => {
    let s = reduce(live(), fx.message_user);
    s = reduce(s, fx.message_assistant);
    s = reduce(s, { ...fx.message_assistant, seq: 12 });
    expect(s.messages.map((m) => m.id)).toEqual(['u-1', 'a-1']);
  });

  it('ignores messages for other sessions', () => {
    const other = structuredClone(fx.message_user) as any;
    other.payload.payload.session_id = 's9';
    expect(reduce(live(), other).messages).toHaveLength(0);
  });

  it('applies a snapshot then the same message frame without a duplicate', () => {
    const msg = (fx.message_user.payload as any).payload;
    let s = reduce(initialState(), snapshot({ messages: [msg] }));
    s = reduce(s, fx.message_user);
    expect(s.messages.map((m) => m.id)).toEqual(['u-1']);
  });

  it('applies a snapshot then the same permission request without a duplicate', () => {
    const req = (fx.permission_request.payload as any).payload;
    let s = reduce(initialState(), snapshot({ permissions: [req] }));
    s = reduce(s, fx.permission_request);
    expect(s.permissions.map((p) => p.id)).toEqual(['p-1']);
  });

  it('upserts a permission request in place by id', () => {
    const req = (fx.permission_request.payload as any).payload;
    let s = reduce(live(), fx.permission_request);
    s = reduce(s, {
      seq: 12, type: 'permission_request',
      payload: { type: 'updated', payload: { ...req, description: 'Run the tests' } },
    });
    expect(s.permissions).toHaveLength(1);
    expect(s.permissions[0].description).toBe('Run the tests');
  });

  it('busy follows the last assistant finish part and run_complete', () => {
    const open = structuredClone(fx.message_assistant) as any;
    open.payload.payload.parts = open.payload.payload.parts.filter((p: any) => p.type !== 'finish');
    let s = reduce(live(), open);
    expect(s.busy).toBe(true);
    s = reduce(s, fx.run_complete);
    expect(s.busy).toBe(false);
  });

  it('permission_notification removes pending and toasts when answered elsewhere', () => {
    let s = reduce(live(), fx.permission_request);
    expect(s.permissions).toHaveLength(1);
    s = reduce(s, fx.permission_notification);
    expect(s.permissions).toHaveLength(0);
    expect(s.toasts.at(-1)?.text).toBe('Permission answered in the terminal');
  });

  it('no toast when this tab answered', () => {
    let s = reduce(live(), fx.permission_request);
    s = markAnswered(s, 'p-1');
    s = reduce(s, fx.permission_notification);
    expect(s.toasts).toHaveLength(0);
  });

  it('question request and notification', () => {
    let s = reduce(live(), fx.question_batch_request);
    expect(s.question?.id).toBe('q-1');
    s = reduce(s, fx.question_batch_notification);
    expect(s.question).toBeNull();
    expect(s.toasts.at(-1)?.text).toBe('Question answered in the terminal');
  });

  it('session_follow resets messages and requests a reload', () => {
    let s = reduce(live(), fx.message_user);
    s = reduce(s, { seq: 13, type: 'session_follow', payload: { session_id: 's2' } });
    expect(s.sessionId).toBe('s2');
    expect(s.messages).toHaveLength(0);
    expect(s.needsReload).toBe('s2');
  });

  it('gateway_closing ends the connection', () => {
    expect(reduce(live(), { seq: 14, type: 'gateway_closing', payload: {} }).conn).toBe('ended');
  });

  it('web_error toasts', () => {
    const s = reduce(live(), { seq: 15, type: 'web_error', payload: { error: 'agent busy' } });
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'error', text: 'agent busy' });
  });

  it('input mode refusal toasts with the terminal reason', () => {
    const s = reduce(live(), {
      seq: 15, type: 'web_error', payload: { error: "Can't switch while Crush is busy" },
    });
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'error', text: "Can't switch while Crush is busy" });
  });

  it('permission mode, session and lsp updates', () => {
    let s = reduce(live(), fx.permission_mode_changed);
    expect(s.permissionMode).toBe('yolo');
    s = reduce(s, fx.session);
    expect(s.session?.title).toBe('Parser refactor');
    expect(contextFill(s)).toBeCloseTo(56100 / 200000);
    s = reduce(s, fx.lsp_event);
    expect(s.lsp.gopls.diagnostics).toBe(2);
  });

  it('input mode comes from the snapshot and its live frame', () => {
    let s = reduce(initialState(), snapshot({ input_mode: 'plan' }));
    expect(s.inputMode).toBe('plan');
    s = reduce(s, { seq: 11, type: 'input_mode_changed', payload: { mode: 'code' } });
    expect(s.inputMode).toBe('code');
  });

  it('snapshot seeds lsp and mcp, and mcp_event updates them', () => {
    let s = reduce(initialState(), snapshot({
      lsp: { gopls: { state: '2', diagnostic_count: 2 } },
      mcp: { fs: { state: 'connected', tool_count: 3 } },
    }));
    expect(s.lsp.gopls).toEqual({ state: '2', diagnostics: 2, tools: 0 });
    expect(s.mcp.fs).toEqual({ state: 'connected', diagnostics: 0, tools: 3 });
    s = reduce(s, {
      seq: 16, type: 'mcp_event',
      payload: { type: 'updated', payload: { type: 'state_changed', name: 'fs', state: 'error', tool_count: 0 } },
    });
    expect(s.mcp.fs.state).toBe('error');
  });

  it('a snapshot keeps toasts but replaces entities', () => {
    let s = reduce(live(), { seq: 15, type: 'web_error', payload: { error: 'x' } });
    s = reduce(s, snapshot({ current_session_id: 's3' }));
    expect(s.toasts).toHaveLength(1);
    expect(s.sessionId).toBe('s3');
  });

  it('helpers', () => {
    let s = reduce(live(), fx.message_tool_result);
    expect(toolResults(s.messages).get('tc-1')?.content).toBe('package parser');
    s = setConn(s, 'reconnecting');
    expect(s.conn).toBe('reconnecting');
    expect(adoptSession(s, 's7').sessionId).toBe('s7');
    expect(needsAttention(s)).toBe(false);
    expect(needsAttention(reduce(s, fx.permission_request))).toBe(true);
    expect(needsAttention(reduce(s, fx.question_batch_request))).toBe(true);
  });
});

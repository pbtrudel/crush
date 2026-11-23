import { describe, expect, it } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame, ModelInfo, Snapshot } from '../api/types';
import {
  adoptSession, contextFill, initialState, inspect, markAnswered, mergeInspected, needsAttention, reduce,
  setConn, startShell, toolResults,
} from './reducer';

const fx = fixtures as unknown as Record<string, Frame>;
const snapshot = (over: Partial<Snapshot> = {}): Frame => ({
  seq: 10,
  type: 'snapshot',
  payload: {
    version: 'v1', working_dir: '/repo/holodeck', current_session_id: 's1',
    messages: [], permissions: [], permission_mode: 'normal', input_mode: 'code',
    model: { id: 'm', name: 'Model', provider: 'p', context_window: 200000, agent_ready: true },
    agent_ready: true, busy: false, queued: [], ...over,
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

  it('a second question batch waits instead of replacing the first', () => {
    const second = structuredClone(fx.question_batch_request) as any;
    second.payload.payload.id = 'q-2';
    second.payload.payload.session_id = 's2';
    let s = reduce(live(), fx.question_batch_request);
    s = reduce(s, second);
    expect(s.question?.id).toBe('q-1');
    expect(s.questionQueue.map((q) => q.id)).toEqual(['q-2']);

    s = reduce(s, fx.question_batch_notification);
    expect(s.question?.id).toBe('q-2');
    expect(s.questionQueue).toHaveLength(0);
  });

  it('a notification for a queued batch keeps the one on screen', () => {
    const second = structuredClone(fx.question_batch_request) as any;
    second.payload.payload.id = 'q-2';
    let s = reduce(live(), fx.question_batch_request);
    s = reduce(s, second);
    s = reduce(s, {
      seq: 14, type: 'question_batch_notification', payload: { type: 'updated', payload: { batch_id: 'q-2' } },
    } as Frame);
    expect(s.question?.id).toBe('q-1');
    expect(s.questionQueue).toHaveLength(0);
  });

  it('a snapshot restores every open batch', () => {
    const req = (fx.question_batch_request.payload as any).payload;
    const s = reduce(initialState(), snapshot({ questions: [req, { ...req, id: 'q-2' }] } as Partial<Snapshot>));
    expect(s.question?.id).toBe('q-1');
    expect(s.questionQueue.map((q) => q.id)).toEqual(['q-2']);
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

  it('does not repeat an error toast for a replayed frame', () => {
    const f = { seq: 11, type: 'web_error', payload: { error: 'Prompt failed' } };
    const s = reduce(reduce(live(), f), f);
    expect(s.toasts.filter((t) => t.text === 'Prompt failed')).toHaveLength(1);
    expect(s.lastSeq).toBe(11);
  });

  it('toasts a new error that happens to repeat the same text', () => {
    const f = { seq: 11, type: 'web_error', payload: { error: 'Prompt failed' } };
    const s = reduce(reduce(live(), f), { ...f, seq: 12 });
    expect(s.toasts.filter((t) => t.text === 'Prompt failed')).toHaveLength(2);
    expect(s.lastSeq).toBe(12);
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

describe('M2 state', () => {
  const fromSnapshot = () => reduce(initialState(), fx.snapshot);

  it('applies the team, the runs and the files from the snapshot', () => {
    const s = fromSnapshot();
    expect(s.team.members.map((m) => m.name)).toEqual(['alice']);
    expect(s.runs.map((r) => r.session_id)).toEqual(['sub-1', 'team-m-1']);
    expect(s.files[0]).toMatchObject({ path: '/repo/parser.go', additions: 3, removals: 1 });
  });

  it('replaces the roster and the runs from their frames', () => {
    const s = reduce(reduce(live(), fx.team_state), fx.agent_runs);
    expect(s.team.tasks.map((t) => t.id)).toEqual(['task-1', 'task-2']);
    expect(s.runs).toHaveLength(2);
  });

  it('marks the file list stale on a file frame', () => {
    expect(reduce(live(), fx.file).filesStale).toBe(true);
  });

  it('records the newest call of another session without touching the log', () => {
    const s = reduce(live(), fx.message_child);
    expect(s.activity['sub-1']).toEqual({ callId: 'sub-tc-1', tool: 'grep' });
    expect(s.messages).toEqual([]);
  });

  it('keeps the inspected transcript live', () => {
    const s = reduce(inspect(live(), 'sub-1'), fx.message_child);
    expect(s.inspected?.messages.map((m) => m.id)).toEqual(['sub-a-1']);
    expect(s.inspected?.loading).toBe(true);
  });

  it('merges live frames that arrived during the transcript fetch', () => {
    let s = reduce(inspect(live(), 'sub-1'), fx.message_child);
    const liveMsg = s.inspected!.messages[0];
    const stale = { ...liveMsg, updated_at: liveMsg.updated_at - 1, parts: [] };
    const earlier = { ...liveMsg, id: 'sub-u-0', role: 'user' as const, created_at: 1, updated_at: 1, parts: [] };
    s = mergeInspected(s, 'sub-1', [earlier, stale], null);
    expect(s.inspected?.messages.map((m) => m.id)).toEqual(['sub-u-0', 'sub-a-1']);
    expect(s.inspected?.messages[1].parts).toHaveLength(2);
    expect(s.inspected?.loading).toBe(false);
  });

  it('ignores a fetch for an agent that is no longer inspected', () => {
    const s = inspect(inspect(live(), 'sub-1'), 'team-m-1');
    expect(mergeInspected(s, 'sub-1', [], null)).toBe(s);
  });

  it('inspecting the main session needs no fetch', () => {
    expect(inspect(live(), 's1').inspected).toMatchObject({ sessionId: 's1', loading: false });
    expect(inspect(inspect(live(), 's1'), null).inspected).toBeNull();
  });

  it('forgets agents and files when the terminal switches session', () => {
    const s = adoptSession(reduce(inspect(fromSnapshot(), 'sub-1'), fx.message_child), 's2');
    expect(s.inspected).toBeNull();
    expect(s.activity).toEqual({});
    expect(s.files).toEqual([]);
  });
});

describe('M3 state', () => {
  const fromSnapshot = () => reduce(initialState(), fx.snapshot);

  it('applies the wire model fields the fixtures carry', () => {
    const m = fx.model_changed.payload as ModelInfo;
    expect(m).toMatchObject({
      id: 'claude-sonnet', provider: 'anthropic', context_window: 200000,
      can_reason: true, effort_levels: ['low', 'medium', 'high'], reasoning_effort: 'high',
      think: true, small: { id: 'claude-haiku', provider: 'anthropic' }, agent_ready: true,
    });
  });

  it('applies model_changed and agent readiness', () => {
    let s = reduce(fromSnapshot(), fx.model_changed);
    expect(s.model.id).toBe('claude-sonnet');
    expect(s.agentReady).toBe(true);
    const payload: ModelInfo = {
      id: 'm2', name: 'Model Two', provider: 'p1', context_window: 1000, agent_ready: false,
    };
    s = reduce(s, { seq: 50, type: 'model_changed', payload });
    expect(s.model.id).toBe('m2');
    expect(s.agentReady).toBe(false);
  });

  it('streams shell output into its run and settles it', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'ls');
    s = reduce(s, { seq: 51, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', chunk: 'a\n' } });
    s = reduce(s, { seq: 52, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', chunk: 'b\n' } });
    s = reduce(s, { seq: 53, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', done: true, exit_code: 0 } });
    expect(s.shells.r1).toEqual({ sessionId: 's1', command: 'ls', output: 'a\nb\n', done: true, exitCode: 0 });
  });

  it('keeps shell output from another tab as its own run', () => {
    const s = reduce(fromSnapshot(), { seq: 54, type: 'shell_output', payload: { run_id: 'r9', session_id: 's1', chunk: 'x' } });
    expect(s.shells.r9.output).toBe('x');
    expect(s.shells.r9.command).toBe('');
  });

  it('drops the shells of the session the tab leaves behind', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'ls');
    s = reduce(s, { seq: 62, type: 'session_follow', payload: { session_id: 's2' } });
    expect(s.sessionId).toBe('s2');
    expect(s.shells).toEqual({});
  });

  it('caps a shell run at 256 KiB of output on a line boundary', () => {
    let s = startShell(fromSnapshot(), 'r2', 's1', 'yes');
    // The odd split lands the 256 KiB cut inside a line, so only the text
    // after the first newline can survive.
    s = reduce(s, { seq: 55, type: 'shell_output', payload: { run_id: 'r2', session_id: 's1', chunk: 'abc\n'.repeat(35_000) + 'ab' } });
    s = reduce(s, { seq: 56, type: 'shell_output', payload: { run_id: 'r2', session_id: 's1', chunk: 'abc\n'.repeat(35_000) } });
    const out = s.shells.r2.output;
    expect(out.startsWith('abc\n')).toBe(true);
    expect(out.length).toBeLessThanOrEqual(256 * 1024);
    expect(out.endsWith('abc\n')).toBe(true);
  });

  it('keeps output that is exactly the cap unchanged', () => {
    const chunk = 'q'.repeat(256 * 1024);
    const s = reduce(startShell(fromSnapshot(), 'r3', 's1', 'yes'), {
      seq: 57, type: 'shell_output', payload: { run_id: 'r3', session_id: 's1', chunk },
    });
    expect(s.shells.r3.output).toBe(chunk);
  });

  it('keeps a tail with no newline in it', () => {
    const s = reduce(startShell(fromSnapshot(), 'r4', 's1', 'yes'), {
      seq: 58, type: 'shell_output', payload: { run_id: 'r4', session_id: 's1', chunk: 'a'.repeat(256 * 1024 + 10) },
    });
    expect(s.shells.r4.output).toBe('a'.repeat(256 * 1024));
  });

  it('keeps a tail whose only newline is its last character', () => {
    const s = reduce(startShell(fromSnapshot(), 'r5', 's1', 'echo'), {
      seq: 59, type: 'shell_output', payload: { run_id: 'r5', session_id: 's1', chunk: 'x'.repeat(256 * 1024 + 5) + '\n' },
    });
    expect(s.shells.r5.output).toBe('x'.repeat(256 * 1024 - 1) + '\n');
  });

  it('records and clears the pending plan', () => {
    let s = reduce(fromSnapshot(), { seq: 57, type: 'plan_ready', payload: { session_id: 's1' } });
    expect(s.planReady).toBe('s1');
    s = reduce(s, { seq: 58, type: 'plan_ready', payload: { session_id: '' } });
    expect(s.planReady).toBe('');
  });

  it('shows agent_starting web errors as a warning, not an error', () => {
    const s = reduce(fromSnapshot(), { seq: 59, type: 'web_error', payload: { session_id: 's1', code: 'agent_starting', error: 'Crush is starting the agent; send the prompt again in a moment' } });
    expect(s.toasts.at(-1)?.tone).toBe('warn');
  });

  it('keeps other web errors an error', () => {
    const s = reduce(fromSnapshot(), { ...fx.web_error, seq: 61 });
    expect(s.toasts.at(-1)?.tone).toBe('error');
  });

  it('does not repeat a replayed agent_starting warning', () => {
    const f: Frame = { seq: 59, type: 'web_error', payload: { code: 'agent_starting', error: 'starting' } };
    const s = reduce(reduce(fromSnapshot(), f), f);
    expect(s.toasts).toHaveLength(1);
  });

  it('takes agent_ready and the pending plan from the snapshot', () => {
    const snap = { ...(fx.snapshot.payload as Snapshot), agent_ready: false, plan_ready_session_id: 's1' };
    const s = reduce(initialState(), { seq: 60, type: 'snapshot', payload: snap });
    expect(s.agentReady).toBe(false);
    expect(s.planReady).toBe('s1');
  });

  it('applies the shell_output and plan_ready fixtures', () => {
    let s = reduce(fromSnapshot(), fx.shell_output);
    expect(s.shells['run-1']).toEqual({
      sessionId: 's1', command: '', output: 'line 1\n', done: false, exitCode: null,
    });
    s = reduce(s, fx.plan_ready);
    expect(s.planReady).toBe('s1');
  });

  it('starts ready with no plan and no shells', () => {
    const s = initialState();
    expect(s.agentReady).toBe(true);
    expect(s.planReady).toBe('');
    expect(s.shells).toEqual({});
  });

  it('follows the snapshot model readiness and keeps shells within a session', () => {
    let s = reduce(initialState(), snapshot());
    expect(s.agentReady).toBe(true);
    expect(s.planReady).toBe('');
    s = startShell(s, 'r1', 's1', 'ls');
    s = reduce(s, snapshot({ current_session_id: 's1' }));
    expect(s.shells.r1.command).toBe('ls');
    s = reduce(s, snapshot({ current_session_id: 's2' }));
    expect(s.shells).toEqual({});
  });
});

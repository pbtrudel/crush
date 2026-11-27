import { describe, expect, it } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame, ModelInfo, SignInState, Snapshot } from '../api/types';
import {
  adoptSession, applySignIn, clearAwsSso, contextFill, initialState, inspect, markAnswered, mergeInspected,
  needsAttention, needsYou, planPending, reduce, setConn, startShell, toolResults, type State,
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
// shellMessage is the user message the workspace persists for a finished
// shell command, one shell_command part per command.
const shellMessage = (sessionId: string, ...commands: string[]): Frame => ({
  seq: 78,
  type: 'message',
  payload: {
    type: 'created',
    payload: {
      id: `m-${commands.join('-')}`, role: 'user', session_id: sessionId,
      parts: commands.map((command) => ({
        type: 'shell_command', data: { command, output: `${command} output`, exit_code: 0 },
      })),
      model: '', provider: '', created_at: 0, updated_at: 0,
    },
  },
});

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

  it('takes the display from display_changed and from the snapshot', () => {
    const solid = { theme: 'solid' as const, motion: 'off' as const, low_power: true, cinematic_hint: false };
    let s = reduce(initialState(), { seq: 1, type: 'display_changed', payload: solid } as Frame);
    expect(s.display).toEqual(solid);
    // A different set proves the snapshot replaces the display rather than
    // leaving the frame's value alone.
    const reset = { theme: 'holodeck' as const, motion: '' as const, low_power: false, cinematic_hint: true };
    s = reduce(s, {
      seq: 2, type: 'snapshot', payload: { ...(fx.snapshot.payload as Snapshot), display: reset },
    } as Frame);
    expect(s.display).toEqual(reset);
  });

  it('keeps the current display when a snapshot carries none', () => {
    const solid = { theme: 'solid' as const, motion: 'off' as const, low_power: true, cinematic_hint: false };
    const payload: Partial<Snapshot> = structuredClone(fx.snapshot.payload as Snapshot);
    delete payload.display;
    const s = reduce({ ...initialState(), display: solid }, { seq: 2, type: 'snapshot', payload } as Frame);
    expect(s.display).toEqual(solid);
  });

  it('names why the gateway closed, and forgets it once the link is live', () => {
    let s = reduce(initialState(), { seq: 1, type: 'gateway_closing', payload: { reason: 'detached' } } as Frame);
    expect(s.endedReason).toBe('detached');
    expect(s.conn).toBe('ended');
    s = reduce(initialState(), { seq: 1, type: 'gateway_closing', payload: { reason: 'crush exited' } } as Frame);
    expect(s.endedReason).toBe('exited');
    s = reduce(s, { seq: 2, type: 'snapshot', payload: fx.snapshot.payload } as Frame);
    expect(s.endedReason).toBeNull();
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
    expect(s.shells.r1).toEqual({ sessionId: 's1', command: 'ls', output: 'a\nb\n', done: true, exitCode: 0, error: null });
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

  it('keeps the early frames of a run whose start lands afterwards', () => {
    let s = reduce(fromSnapshot(), { seq: 70, type: 'shell_output', payload: { run_id: 'r6', session_id: 's1', chunk: 'early\n' } });
    s = reduce(s, { seq: 71, type: 'shell_output', payload: { run_id: 'r6', session_id: 's1', done: true, exit_code: 2, error: 'The shell command failed' } });
    s = startShell(s, 'r6', 's1', 'boom');
    expect(s.shells.r6).toEqual({
      sessionId: 's1', command: 'boom', output: 'early\n', done: true, exitCode: 2, error: 'The shell command failed',
    });
  });

  it('stores the failure the done frame carries', () => {
    const s = reduce(startShell(fromSnapshot(), 'r7', 's1', 'boom'), {
      seq: 72, type: 'shell_output', payload: { run_id: 'r7', session_id: 's1', done: true, exit_code: 1, error: 'The shell command failed' },
    });
    expect(s.shells.r7.error).toBe('The shell command failed');
  });

  it('keeps the deck idle when a persisted shell message arrives', () => {
    // A bang command's result is persisted as a user message, but it starts
    // no agent turn, so it must not read as a busy session.
    const s = reduce(live(), shellMessage('s1', 'ls'));
    expect(s.busy).toBe(false);
  });

  it('drops a done run when its persisted shell message arrives', () => {
    let s = startShell(fromSnapshot(), 'r8', 's1', 'ls');
    s = reduce(s, { seq: 73, type: 'shell_output', payload: { run_id: 'r8', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r8).toBeUndefined();
  });

  it('drops one done run per persisted part, oldest first', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'ls');
    s = reduce(s, { seq: 74, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', done: true, exit_code: 0 } });
    s = startShell(s, 'r2', 's1', 'ls');
    s = reduce(s, { seq: 75, type: 'shell_output', payload: { run_id: 'r2', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r1).toBeUndefined();
    expect(s.shells.r2).not.toBeUndefined();
  });

  it('keeps a run the persisted message does not match', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'make');
    s = reduce(s, { seq: 76, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r1).not.toBeUndefined();
  });

  it('keeps a run that is still streaming when the persisted message arrives', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'ls');
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r1).not.toBeUndefined();
  });

  it('drops the card when the done frame lands after the persisted message', () => {
    // The workspace persists the result before the gateway publishes done,
    // so the message can win the race to the tab.
    let s = startShell(fromSnapshot(), 'r9', 's1', 'ls');
    s = reduce(s, { seq: 79, type: 'shell_output', payload: { run_id: 'r9', session_id: 's1', chunk: 'a\n' } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r9).not.toBeUndefined();

    s = reduce(s, { seq: 80, type: 'shell_output', payload: { run_id: 'r9', session_id: 's1', done: true, exit_code: 0 } });
    expect(s.shells.r9).toBeUndefined();
  });

  it('ignores a shell message for another session', () => {
    let s = startShell(fromSnapshot(), 'r1', 's1', 'ls');
    s = reduce(s, { seq: 77, type: 'shell_output', payload: { run_id: 'r1', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s2', 'ls'));
    expect(s.shells.r1).not.toBeUndefined();
  });

  it('leaves no run when the start answer lands after its message and done', () => {
    // A fast command can deliver chunk, message and done before the 202
    // names the run.
    let s = reduce(fromSnapshot(), { seq: 81, type: 'shell_output', payload: { run_id: 'r10', session_id: 's1', chunk: 'a\n' } });
    s = reduce(s, shellMessage('s1', 'ls'));
    s = reduce(s, { seq: 82, type: 'shell_output', payload: { run_id: 'r10', session_id: 's1', done: true, exit_code: 0 } });
    s = startShell(s, 'r10', 's1', 'ls');
    expect(s.shells.r10).toBeUndefined();
  });

  it('leaves no run when the start answer lands before its done', () => {
    let s = reduce(fromSnapshot(), { seq: 83, type: 'shell_output', payload: { run_id: 'r11', session_id: 's1', chunk: 'a\n' } });
    s = reduce(s, shellMessage('s1', 'ls'));
    s = startShell(s, 'r11', 's1', 'ls');
    s = reduce(s, { seq: 84, type: 'shell_output', payload: { run_id: 'r11', session_id: 's1', done: true, exit_code: 0 } });
    expect(s.shells.r11).toBeUndefined();
  });

  it('drops another tab run with no command through its persisted message', () => {
    let s = reduce(fromSnapshot(), { seq: 85, type: 'shell_output', payload: { run_id: 'r12', session_id: 's1', done: true, exit_code: 0 } });
    expect(s.shells.r12.command).toBe('');
    s = reduce(s, shellMessage('s1', 'make'));
    expect(s.shells.r12).toBeUndefined();
  });

  it('never drops a run of another session through a message', () => {
    let s = reduce(fromSnapshot(), { seq: 86, type: 'shell_output', payload: { run_id: 'r13', session_id: 's2', done: true, exit_code: 0 } });
    expect(s.shells.r13.sessionId).toBe('s2');
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.shells.r13).not.toBeUndefined();
  });

  it('ignores a frame for a run its persisted message already dropped', () => {
    let s = startShell(fromSnapshot(), 'r14', 's1', 'ls');
    s = reduce(s, { seq: 87, type: 'shell_output', payload: { run_id: 'r14', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.droppedShells.r14).toBe(true);

    s = reduce(s, { seq: 88, type: 'shell_output', payload: { run_id: 'r14', session_id: 's1', chunk: 'late\n' } });
    expect(s.shells.r14).toBeUndefined();
  });

  it('forgets dropped shells when the tab adopts another session', () => {
    let s = reduce(fromSnapshot(), { seq: 89, type: 'shell_output', payload: { run_id: 'r15', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    expect(s.droppedShells.r15).toBe(true);
    expect(adoptSession(s, 's2').droppedShells).toEqual({});
  });

  it('keeps dropped shells when a snapshot repeats their session', () => {
    let s = reduce(fromSnapshot(), { seq: 90, type: 'shell_output', payload: { run_id: 'r16', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    s = reduce(s, snapshot());
    expect(s.droppedShells.r16).toBe(true);
  });

  it('forgets dropped shells when a snapshot names another session', () => {
    let s = reduce(fromSnapshot(), { seq: 91, type: 'shell_output', payload: { run_id: 'r17', session_id: 's1', done: true, exit_code: 0 } });
    s = reduce(s, shellMessage('s1', 'ls'));
    s = reduce(s, snapshot({ current_session_id: 's2' }));
    expect(s.droppedShells).toEqual({});
    expect(s.shells).toEqual({});
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
      sessionId: 's1', command: '', output: 'line 1\n', done: false, exitCode: null, error: null,
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

describe('M3 notifications', () => {
  const fromSnapshot = () => reduce(initialState(), snapshot());
  const agentEvent = (seq: number, payload: Record<string, unknown>): Frame => ({
    seq, type: 'agent_event', payload: { type: 'created', payload },
  });
  const configStale = (seq: number): Frame => ({
    seq, type: 'config_stale', payload: { type: 'updated', payload: { changed: ['/repo/crushrc'], missing: [] } },
  });
  const updateAvailable = (seq: number, over: Record<string, unknown> = {}): Frame => ({
    seq,
    type: 'update_available',
    payload: {
      type: 'updated',
      payload: { current_version: '0.1.0', latest_version: '0.2.0', is_development: false, ...over },
    },
  });

  it('toasts what an agent event says', () => {
    const s = reduce(fromSnapshot(), agentEvent(30, {
      type: 'agent_finished', session_id: 's2', session_title: 'Lexer',
    }));
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'info', text: 'Turn complete in “Lexer”' });
  });

  it('does not repeat an agent event toast for a replayed frame', () => {
    const f = agentEvent(31, { type: 'error', error: 'rate limited' });
    const s = reduce(reduce(fromSnapshot(), f), f);
    expect(s.toasts).toHaveLength(1);
  });

  it('reads hidden from the state so a visible session still toasts in a hidden tab', () => {
    const base = { ...fromSnapshot(), hidden: true };
    const s = reduce(base, agentEvent(32, { type: 'agent_finished', session_id: 's1', session_title: 'Main' }));
    expect(s.toasts.at(-1)?.text).toBe('Turn complete in “Main”');
  });

  it('toasts a stale configuration once per tab, through a snapshot and a session switch', () => {
    let s = reduce(fromSnapshot(), configStale(33));
    expect(s.toasts.at(-1)).toMatchObject({
      tone: 'info', text: 'Configuration changed on disk. Reload it from the palette.',
    });
    s = reduce(s, snapshot({ current_session_id: 's3' }));
    s = reduce(s, configStale(34));
    expect(s.toasts.filter((t) => t.text === 'Configuration changed on disk. Reload it from the palette.')).toHaveLength(1);
  });

  it('toasts a new version once per tab with the terminal wording', () => {
    let s = reduce(fromSnapshot(), updateAvailable(35));
    expect(s.toasts.at(-1)).toMatchObject({
      tone: 'info', text: 'Crush update available: v0.1.0 → v0.2.0.',
    });
    s = reduce(s, updateAvailable(36));
    expect(s.toasts).toHaveLength(1);
  });

  it('names a development build in the update toast', () => {
    const s = reduce(fromSnapshot(), updateAvailable(37, { is_development: true }));
    expect(s.toasts.at(-1)?.text).toBe('This is a development version of Crush. The latest version is v0.2.0.');
  });

  it('ignores an update frame with no payload', () => {
    const s = reduce(fromSnapshot(), { seq: 38, type: 'update_available', payload: undefined });
    expect(s.toasts).toHaveLength(0);
    expect(s.announced.update_available).toBeUndefined();
  });

  // These read the real-wire fixtures the gateway writes, so a wire change
  // to one of the three frames reaches a web test instead of passing unseen.
  // Each fixture carries seq 1, which the snapshot's seq 10 already replayed,
  // so the tests hand each frame a new seq.
  it('toasts the real agent error frame', () => {
    const s = reduce(fromSnapshot(), { ...fx.agent_event, seq: 39 });
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'error', text: 'The provider refused the request' });
  });

  it('toasts the real stale config frame', () => {
    const s = reduce(fromSnapshot(), { ...fx.config_stale, seq: 40 });
    expect(s.toasts.at(-1)).toMatchObject({
      tone: 'info', text: 'Configuration changed on disk. Reload it from the palette.',
    });
  });

  it('toasts the real update frame with both versions', () => {
    const s = reduce(fromSnapshot(), { ...fx.update_available, seq: 41 });
    expect(s.toasts.at(-1)).toMatchObject({
      tone: 'info', text: 'Crush update available: v0.30.0 → v0.31.0.',
    });
  });
});

describe('the M4a wire state', () => {
  const fromSnapshot = () => reduce(initialState(), fx.snapshot);

  it('takes secure prompts, sign-in and AWS SSO from the snapshot', () => {
    const snap = { ...(fx.snapshot.payload as Snapshot),
      secure_prompts: [{ source: 'ssh', id: 's1', kind: 'password', title: 'SSH', message: 'Password for git' }],
      signin: { flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'waiting', kind: 'device', user_code: 'WDJB-MJHT' },
      aws_sso: { command: 'aws sso login' } };
    const s = reduce(initialState(), { seq: 70, type: 'snapshot', payload: snap });
    expect(s.securePrompts.map((p) => p.id)).toEqual(['s1']);
    expect(s.signin?.user_code).toBe('WDJB-MJHT');
    expect(s.awsSso).toEqual({ command: 'aws sso login' });
    expect(needsAttention(s)).toBe(true);
  });

  it('toasts once when prompts close that this tab did not answer', () => {
    let s = reduce(fromSnapshot(), fx.secure_prompt as Frame);
    s = reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } });
    expect(s.securePrompts).toEqual([]);
    expect(s.toasts.filter((t) => t.text === 'Answered in the terminal')).toHaveLength(1);
  });

  it('stays quiet when this tab answered', () => {
    let s = reduce(fromSnapshot(), fx.secure_prompt as Frame);
    for (const p of s.securePrompts) s = markAnswered(s, p.id);
    const before = s.toasts.length;
    s = reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } });
    expect(s.toasts.length).toBe(before);
  });

  it('keeps answeredHere through a snapshot and does not toast its own answer', () => {
    let s = reduce(fromSnapshot(), fx.secure_prompt as Frame);
    const [prompt] = s.securePrompts;
    s = markAnswered(s, prompt.id);
    const before = s.toasts.length;

    // A snapshot lands between the tab's answer and the frame that closes
    // the prompt, and it still carries the prompt.
    s = reduce(s, {
      seq: s.lastSeq + 1,
      type: 'snapshot',
      payload: { ...(fx.snapshot.payload as Snapshot), secure_prompts: [prompt] },
    });
    expect(s.answeredHere).toEqual({ [prompt.id]: true });

    // The prompt was answered here, so its closing frame is not news.
    s = reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } });
    expect(s.securePrompts).toEqual([]);
    expect(s.toasts.length).toBe(before);
  });

  it('keeps only the answers a snapshot still shows', () => {
    const snap = fx.snapshot.payload as Snapshot;
    let s: State = {
      ...fromSnapshot(),
      answeredHere: { 'a-prompt': true, 'a-perm': true, 'a-question': true, gone: true },
    };
    s = reduce(s, {
      seq: s.lastSeq + 1,
      type: 'snapshot',
      payload: {
        ...snap,
        secure_prompts: [{ source: 'ssh', id: 'a-prompt', kind: 'password', title: 'SSH', message: 'Password for git' }],
        permissions: [{
          id: 'a-perm', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
          description: 'Run the tests', action: 'execute', params: {}, path: '/repo',
        }],
        questions: [{ id: 'a-question', session_id: 's1', tool_call_id: 'tc1', questions: [] }],
      },
    });

    expect(s.answeredHere).toEqual({ 'a-prompt': true, 'a-perm': true, 'a-question': true });
  });

  it('shows a touch prompt as a notice and stays quiet when the terminal confirms it', () => {
    const touch = { source: 'ssh', id: 't1', kind: 'touch', title: 'Security key', message: 'Tap your security key to continue.' };
    let s = reduce(fromSnapshot(), { seq: 80, type: 'secure_prompt', payload: { prompts: [touch] } });
    expect(needsAttention(s)).toBe(false);
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'warn', text: 'Tap your security key to continue.' });
    // The TUI answers every touch prompt at once (internal/ui/model/ssh.go).
    const before = s.toasts.length;
    s = reduce(s, { seq: 81, type: 'secure_prompt', payload: { prompts: [] } });
    expect(s.toasts.length).toBe(before);
  });

  it('follows the sign-in frame and announces a finished sign-in once', () => {
    let s = reduce(fromSnapshot(), fx.signin as Frame);
    expect(s.signin?.state).toBe('waiting');
    const done = { ...(fx.signin.payload as SignInState), state: 'done' as const };
    s = reduce(s, { seq: s.lastSeq + 1, type: 'signin', payload: done });
    s = reduce(s, { seq: s.lastSeq + 1, type: 'signin', payload: done });
    expect(s.toasts.filter((t) => t.text === 'Signed in to GitHub Copilot.').length).toBe(1);
  });

  it('ignores a replayed sign-in frame', () => {
    let s = reduce(fromSnapshot(), { ...(fx.signin as Frame), seq: 5 });
    const done = { ...(fx.signin.payload as SignInState), flow_id: 'f-old', state: 'done' as const };
    s = reduce(s, { seq: 5, type: 'signin', payload: done });
    expect(s.signin?.flow_id).toBe('f1');
    expect(s.signin?.state).toBe('waiting');
    expect(s.toasts.filter((t) => t.text === 'Signed in to GitHub Copilot.')).toHaveLength(0);
  });

  it('opens, updates and closes the AWS SSO card', () => {
    let s = reduce(fromSnapshot(), fx.agent_event_aws_sso as Frame);
    expect(s.awsSso?.command).toBe('aws sso login --profile dev');
    s = reduce(s, { ...(fx.agent_event_aws_sso_url as Frame), seq: s.lastSeq + 1 });
    expect(s.awsSso?.url).toContain('amazonaws.com');
    s = reduce(s, { ...(fx.agent_event_aws_sso_result as Frame), seq: s.lastSeq + 1 });
    expect(s.awsSso?.error).toBe('the SSO session expired');
  });

  it('starts a fresh AWS SSO card after a failed refresh', () => {
    let s = reduce(fromSnapshot(), fx.agent_event_aws_sso as Frame);
    s = reduce(s, { ...(fx.agent_event_aws_sso_url as Frame), seq: s.lastSeq + 1 });
    s = reduce(s, { ...(fx.agent_event_aws_sso_result as Frame), seq: s.lastSeq + 1 });
    s = reduce(s, { ...(fx.agent_event_aws_sso as Frame), seq: s.lastSeq + 1 });
    expect(s.awsSso).toEqual({ command: 'aws sso login --profile dev' });
  });

  it('clears the AWS SSO card when the refresh succeeds', () => {
    let s = reduce(fromSnapshot(), fx.agent_event_aws_sso as Frame);
    const ok = structuredClone(fx.agent_event_aws_sso_result) as any;
    delete ok.payload.payload.error;
    s = reduce(s, { ...ok, seq: s.lastSeq + 1 });
    expect(s.awsSso).toBeNull();
  });

  it('ignores a replayed AWS SSO frame', () => {
    const s = reduce(fromSnapshot(), { ...(fx.agent_event_aws_sso as Frame), seq: 5 });
    const replay = reduce(s, { ...(fx.agent_event_aws_sso_url as Frame), seq: 5 });
    expect(replay.awsSso?.url).toBeUndefined();
    expect(replay.lastSeq).toBe(5);
  });

  it('records the state a start answered with without waiting for its frame', () => {
    const s = applySignIn(fromSnapshot(), {
      flow_id: 'f2', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'waiting',
      kind: 'device', user_code: 'WDJB-MJHT',
    });
    expect(s.signin?.user_code).toBe('WDJB-MJHT');
    expect(s.toasts.some((t) => t.text.startsWith('Signed in to'))).toBe(false);
  });

  it('drops a start answer that lands after the flow finished', () => {
    const done: SignInState = {
      flow_id: 'f9', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'done',
    };
    const late: SignInState = {
      ...done, state: 'waiting', kind: 'device', user_code: 'WDJB-MJHT',
    };
    const s = applySignIn(applySignIn(fromSnapshot(), done), late);
    expect(s.signin?.state).toBe('done');
    expect(s.signin?.user_code).toBeUndefined();
  });

  it('dismisses an errored AWS SSO card and opens a fresh one on the next command', () => {
    let s = reduce(fromSnapshot(), fx.agent_event_aws_sso as Frame);
    s = reduce(s, { ...(fx.agent_event_aws_sso_result as Frame), seq: s.lastSeq + 1 });
    expect(s.awsSso?.error).toBe('the SSO session expired');
    s = clearAwsSso(s);
    expect(s.awsSso).toBeNull();
    s = reduce(s, { ...(fx.agent_event_aws_sso as Frame), seq: s.lastSeq + 1 });
    expect(s.awsSso).toEqual({ command: 'aws sso login --profile dev' });
  });

  it('offers a sign-in action when a login expired', () => {
    const s = reduce(fromSnapshot(), fx.agent_event_reauth as Frame);
    expect(s.toasts.at(-1)).toMatchObject({ tone: 'warn', action: { kind: 'sign-in', provider: 'openai' } });
  });

  it('carries the main model in the re-auth action for its provider', () => {
    const base = { ...fromSnapshot(), model: { ...fromSnapshot().model, provider: 'openai', id: 'gpt-5' } };
    const s = reduce(base, { ...(fx.agent_event_reauth as Frame), seq: base.lastSeq + 1 });
    expect(s.toasts.at(-1)?.action).toEqual({ kind: 'sign-in', provider: 'openai', model: { type: 'main', model: 'gpt-5' } });
  });
});

describe('the pending plan needs you', () => {
  const plan = (): State => reduce(initialState(), snapshot({
    input_mode: 'plan',
    plan_ready_session_id: 's1',
  }));

  it('is pending only for the current session in plan mode', () => {
    let s = plan();
    expect(planPending(s)).toBe(true);
    expect(planPending({ ...s, sessionId: 's2' })).toBe(false);
    expect(planPending({ ...s, inputMode: 'code' })).toBe(false);
    expect(planPending({ ...s, planReady: '' })).toBe(false);
    s = reduce(s, { seq: 40, type: 'plan_ready', payload: { session_id: '' } });
    expect(planPending(s)).toBe(false);
  });

  it('leaves the attention gate untouched while asking for the core look', () => {
    const s = plan();
    expect(needsAttention(s)).toBe(false);
    expect(needsYou(s)).toBe(true);
  });
});

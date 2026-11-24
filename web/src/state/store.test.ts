import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import { ApiError, api } from '../api/http';
import type { Frame, Message, PermissionRequest, QuestionRequest, Session, Snapshot, WireEvent } from '../api/types';
import { effectsLevel } from './effects';
import { STARTING_TEXT } from './failure';
import { adoptSession, initialState } from './reducer';
import { actions, boot, dismissSideAnswer, dispatch, handleFrames, sideAnswer, state, watchVisibility } from './store';
import { emptyTelemetry, telemetry } from './telemetry';
import { forgetToken, setToken, token } from './token';

const fx = fixtures as unknown as Record<string, Frame>;

function batch(): QuestionRequest {
  return {
    id: 'b1', session_id: 's1', tool_call_id: 'tc1',
    questions: [{ id: 'q1', type: 'free_text', question: 'Notes?' }],
  };
}

function permission(): PermissionRequest {
  return {
    id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
    description: 'Run tests', action: 'execute', params: {}, path: '/repo',
  };
}

function seed() {
  state.value = {
    ...initialState(), conn: 'live', sessionId: 's1', question: batch(), permissions: [permission()],
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  forgetToken();
});

describe('actions.cancelQuestion', () => {
  it('drops a batch the terminal already answered', async () => {
    seed();
    vi.spyOn(api, 'cancelQuestion').mockRejectedValue(new ApiError(409, 'not_pending', 'question batch is no longer pending'));
    await expect(actions.cancelQuestion('b1')).resolves.toBeNull();
    expect(state.value.question).toBeNull();
    expect(state.value.answered).toContain('b1');
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });

  it('returns the error text and keeps the batch when the cancel fails', async () => {
    seed();
    vi.spyOn(api, 'cancelQuestion').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await expect(actions.cancelQuestion('b1')).resolves.toBe('gateway on fire');
    expect(state.value.question).not.toBeNull();
  });

  it('drops a batch the service no longer holds', async () => {
    seed();
    vi.spyOn(api, 'cancelQuestion').mockResolvedValue({ resolved: false });
    await expect(actions.cancelQuestion('b1')).resolves.toBeNull();
    expect(state.value.question).toBeNull();
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });
});

describe('actions.grant', () => {
  it('drops a permission the service no longer holds', async () => {
    seed();
    vi.spyOn(api, 'grant').mockResolvedValue({ resolved: false });
    await expect(actions.grant('p1', 'allow')).resolves.toBeNull();
    expect(state.value.permissions).toHaveLength(0);
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });

  it('drops a permission the gateway no longer has pending', async () => {
    seed();
    vi.spyOn(api, 'grant').mockRejectedValue(new ApiError(404, 'not_pending', 'permission request is no longer pending'));
    await expect(actions.grant('p1', 'allow')).resolves.toBeNull();
    expect(state.value.permissions).toHaveLength(0);
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });
});

describe('actions.answer', () => {
  it('names the batch it answers', async () => {
    seed();
    const answer = vi.spyOn(api, 'answer').mockResolvedValue({ resolved: true });
    await expect(actions.answer('b1', [])).resolves.toBeNull();
    expect(answer).toHaveBeenCalledWith('b1', []);
  });

  it('names the batch it cancels', async () => {
    seed();
    const cancel = vi.spyOn(api, 'cancelQuestion').mockResolvedValue({ resolved: true });
    await expect(actions.cancelQuestion('b1')).resolves.toBeNull();
    expect(cancel).toHaveBeenCalledWith('b1');
  });

  it('drops a batch the service no longer holds', async () => {
    seed();
    vi.spyOn(api, 'answer').mockResolvedValue({ resolved: false });
    await expect(actions.answer('b1', [])).resolves.toBeNull();
    expect(state.value.question).toBeNull();
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });

  it('drops a batch the gateway no longer has pending', async () => {
    seed();
    vi.spyOn(api, 'answer').mockRejectedValue(new ApiError(404, 'not_pending', 'question batch is no longer pending'));
    await expect(actions.answer('b1', [])).resolves.toBeNull();
    expect(state.value.question).toBeNull();
    expect(state.value.toasts.map((t) => t.text)).toContain('Already answered in the terminal');
  });

  it('a failed answer still toasts when the terminal answers later', async () => {
    seed();
    vi.spyOn(api, 'answer').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await expect(actions.answer('b1', [])).resolves.toBe('gateway on fire');
    expect(state.value.answered).not.toContain('b1');
    handleFrames([{ seq: 30, type: 'question_batch_notification', payload: { type: 'updated', payload: { batch_id: 'b1', session_id: 's1' } } }]);
    expect(state.value.toasts.map((t) => t.text)).toContain('Question answered in the terminal');
  });

  it('a failed skip still toasts when the terminal answers later', async () => {
    seed();
    vi.spyOn(api, 'cancelQuestion').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await expect(actions.cancelQuestion('b1')).resolves.toBe('gateway on fire');
    expect(state.value.answered).not.toContain('b1');
    handleFrames([{ seq: 31, type: 'question_batch_notification', payload: { type: 'updated', payload: { batch_id: 'b1', session_id: 's1' } } }]);
    expect(state.value.toasts.map((t) => t.text)).toContain('Question answered in the terminal');
  });

  it('a failed grant still toasts when the terminal answers later', async () => {
    seed();
    vi.spyOn(api, 'grant').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await expect(actions.grant('p1', 'allow')).resolves.toBe('gateway on fire');
    expect(state.value.answered).not.toContain('p1');
    handleFrames([{ seq: 32, type: 'permission_notification', payload: { type: 'updated', payload: { tool_call_id: 'tc1', cancelled: false } } }]);
    expect(state.value.toasts.map((t) => t.text)).toContain('Permission answered in the terminal');
  });
});

describe('actions.sendPrompt', () => {
  it('starts a session when the terminal has none', async () => {
    state.value = { ...initialState(), conn: 'live', hydrated: true, sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const send = vi.spyOn(api, 'sendPrompt').mockResolvedValue({ accepted: true });

    await expect(actions.sendPrompt('first task', [])).resolves.toBeNull();

    expect(create).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith('s2', 'first task', []);
  });

  it('waits for the first snapshot instead of starting a session', async () => {
    // A tab can take a prompt before the stream names the session the
    // terminal shows; starting one there would switch the terminal away.
    state.value = { ...initialState(), conn: 'live' };
    const create = vi.spyOn(api, 'createSession');
    const send = vi.spyOn(api, 'sendPrompt').mockResolvedValue({ accepted: true });

    const pending = actions.sendPrompt('first task', []);
    await Promise.resolve();
    expect(create).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();

    handleFrames([fx.snapshot]);
    await expect(pending).resolves.toBeNull();

    expect(create).not.toHaveBeenCalled();
    expect(send).toHaveBeenCalledWith('s1', 'first task', []);
  });

  it('fails without starting a session when the connection ends', async () => {
    // A gateway that comes back later must not find a session the tab
    // started while it was gone.
    state.value = { ...initialState(), conn: 'live' };
    const create = vi.spyOn(api, 'createSession');
    const send = vi.spyOn(api, 'sendPrompt');

    const pending = actions.sendPrompt('first task', []);
    await Promise.resolve();
    handleFrames([{ seq: 1, type: 'gateway_closing', payload: {} }]);
    await expect(pending).resolves.toEqual({ tone: 'error', text: 'The request did not reach crush' });

    expect(create).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
  });

  it('fails without starting a session when the token is refused', async () => {
    state.value = { ...initialState(), conn: 'live' };
    const create = vi.spyOn(api, 'createSession');
    const send = vi.spyOn(api, 'sendPrompt');

    const pending = actions.sendPrompt('first task', []);
    await Promise.resolve();
    dispatch((s) => ({ ...s, conn: 'unauthorized' }));
    await expect(pending).resolves.toEqual({ tone: 'error', text: 'The request did not reach crush' });

    expect(create).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
  });

  it('gives up waiting for a snapshot after the timeout', async () => {
    vi.useFakeTimers();
    try {
      state.value = { ...initialState(), conn: 'live' };
      const create = vi.spyOn(api, 'createSession');
      const send = vi.spyOn(api, 'sendPrompt');

      const pending = actions.sendPrompt('first task', []);
      await Promise.resolve();
      expect(create).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(10_000);
      await expect(pending).resolves.toEqual({ tone: 'error', text: 'The request did not reach crush' });
      expect(create).not.toHaveBeenCalled();

      // A snapshot after the prompt failed sends nothing: the deck already
      // showed the failure and a retry is the user's choice.
      handleFrames([fx.snapshot]);
      await Promise.resolve();
      expect(send).not.toHaveBeenCalled();
      expect(create).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('sends nothing and reports the failure when the session cannot start', async () => {
    state.value = { ...initialState(), conn: 'live', hydrated: true, sessionId: '' };
    vi.spyOn(api, 'createSession').mockRejectedValue(new ApiError(500, 'create_session', 'gateway on fire'));
    const send = vi.spyOn(api, 'sendPrompt');

    await expect(actions.sendPrompt('first task', [])).resolves.toEqual({ tone: 'error', text: 'gateway on fire' });

    expect(send).not.toHaveBeenCalled();
  });

  it('keeps the created session when the prompt fails, so a retry does not start another', async () => {
    state.value = { ...initialState(), conn: 'live', hydrated: true, sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const send = vi.spyOn(api, 'sendPrompt')
      .mockRejectedValueOnce(new ApiError(500, 'http_error', 'gateway on fire'))
      .mockResolvedValueOnce({ accepted: true });

    await expect(actions.sendPrompt('first task', [])).resolves.toEqual({ tone: 'error', text: 'gateway on fire' });
    expect(state.value.sessionId).toBe('s2');

    await expect(actions.sendPrompt('first task', [])).resolves.toBeNull();

    expect(create).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenNthCalledWith(1, 's2', 'first task', []);
    expect(send).toHaveBeenNthCalledWith(2, 's2', 'first task', []);
  });

  it('names a starting agent as a failure a control can style', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1' };
    vi.spyOn(api, 'sendPrompt').mockRejectedValue(new ApiError(503, 'agent_starting', 'not ready'));
    await expect(actions.sendPrompt('task', [])).resolves.toEqual({ tone: 'starting', text: STARTING_TEXT });
  });
});

describe('prompt queue', () => {
  it('clears the chips when the queue is cleared', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', queued: ['first', 'second'] };
    const clear = vi.spyOn(api, 'clearQueue').mockResolvedValue(undefined);
    await actions.clearQueue();
    expect(clear).toHaveBeenCalledWith('s1');
    expect(state.value.queued).toEqual([]);
  });

  it('keeps the chips when clearing fails', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', queued: ['first'] };
    vi.spyOn(api, 'clearQueue').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await actions.clearQueue();
    expect(state.value.queued).toEqual(['first']);
    expect(state.value.toasts.map((t) => t.text)).toContain('gateway on fire');
  });

  it('reloads the queue after queueing a prompt during a run', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', busy: true };
    vi.spyOn(api, 'sendPrompt').mockResolvedValue({ accepted: true });
    const queue = vi.spyOn(api, 'queue').mockResolvedValue({ queued: ['next task'] });
    await expect(actions.sendPrompt('next task', [])).resolves.toBeNull();
    expect(queue).toHaveBeenCalledWith('s1');
    expect(state.value.queued).toEqual(['next task']);
  });

  it('does not ask for the queue when the session is idle', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', busy: false };
    vi.spyOn(api, 'sendPrompt').mockResolvedValue({ accepted: true });
    const queue = vi.spyOn(api, 'queue').mockResolvedValue({ queued: [] });
    await actions.sendPrompt('only task', []);
    expect(queue).not.toHaveBeenCalled();
  });

  it('reloads the queue when a run completes', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', busy: true, queued: ['stale'] };
    const queue = vi.spyOn(api, 'queue').mockResolvedValue({ queued: [] });
    handleFrames([fx.run_complete]);
    await vi.waitFor(() => expect(state.value.queued).toEqual([]));
    expect(queue).toHaveBeenCalledWith('s1');
  });

  it('reloads the queue when a user message arrives', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', queued: [] };
    vi.spyOn(api, 'queue').mockResolvedValue({ queued: ['queued task'] });
    handleFrames([fx.message_user]);
    await vi.waitFor(() => expect(state.value.queued).toEqual(['queued task']));
  });

  it('ignores frames that cannot change the queue', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', queued: ['kept'] };
    const queue = vi.spyOn(api, 'queue').mockResolvedValue({ queued: [] });
    handleFrames([fx.message_assistant]);
    await Promise.resolve();
    expect(queue).not.toHaveBeenCalled();
    expect(state.value.queued).toEqual(['kept']);
  });
});

describe('actions.setInputMode', () => {
  it('posts the requested mode and leaves the state to the frame', async () => {
    state.value = { ...initialState(), conn: 'live', inputMode: 'code' };
    const set = vi.spyOn(api, 'setInputMode').mockResolvedValue(undefined);

    await actions.setInputMode('plan');

    expect(set).toHaveBeenCalledWith('plan');
    expect(state.value.inputMode).toBe('code');
  });

  it('reports a failed switch', async () => {
    state.value = { ...initialState(), conn: 'live', inputMode: 'code' };
    vi.spyOn(api, 'setInputMode').mockRejectedValue(new ApiError(400, 'invalid_mode', 'unknown mode'));

    await actions.setInputMode('plan');

    expect(state.value.toasts.map((t) => t.text)).toContain('unknown mode');
  });
});

describe('telemetry from the stream', () => {
  beforeEach(() => {
    state.value = initialState();
    telemetry.value = emptyTelemetry();
  });

  it('treats a snapshot batch as baseline and records only later calls', () => {
    handleFrames([fx.snapshot]);
    expect(state.value.sessionId).toBe('s1');
    expect(telemetry.value.tools).toEqual([]);
    expect(telemetry.value.seen.has('tc-0')).toBe(true);
    // A reconnect replays the same snapshot: still no activity.
    handleFrames([fx.snapshot]);
    expect(telemetry.value.tools).toEqual([]);
    handleFrames([fx.message_assistant]);
    expect(telemetry.value.tools.map((e) => e.id)).toEqual(['tc-1']);
    expect(telemetry.value.tools[0].category).toBe('read');
  });

  it('records a live call that arrives in its own batch', () => {
    handleFrames([fx.snapshot]);
    handleFrames([fx.message_assistant]);
    handleFrames([fx.message_assistant]);
    expect(telemetry.value.tools).toHaveLength(1);
  });

  it('resets telemetry when a session is adopted through dispatch', () => {
    handleFrames([fx.snapshot]);
    handleFrames([fx.message_assistant]);
    expect(telemetry.value.tools).toHaveLength(1);
    dispatch((s) => adoptSession(s, 's2'));
    expect(telemetry.value).toEqual(emptyTelemetry());
  });
});

describe('actions.effectsReduced', () => {
  it('records the step down and offers to restore the effects', () => {
    state.value = initialState();
    effectsLevel.value = 0;
    actions.effectsReduced(2);
    expect(effectsLevel.value).toBe(2);
    const toast = state.value.toasts.at(-1)!;
    expect(toast.tone).toBe('warn');
    expect(toast.text).toBe('Reduced effects to keep things smooth');
    expect(toast.action).toBe('restore-effects');
    effectsLevel.value = 0;
  });
});

describe('M2 actions', () => {
  it('inspect loads the transcript and the session of a child agent', async () => {
    seed();
    const msgs = [(fx.message_child.payload as WireEvent<Message>).payload];
    vi.spyOn(api, 'messages').mockResolvedValue(msgs);
    vi.spyOn(api, 'session').mockResolvedValue({ id: 'sub-1', title: 'Map the parser' } as Session);
    await actions.inspect('sub-1');
    expect(state.value.inspected).toMatchObject({ sessionId: 'sub-1', loading: false });
    expect(state.value.inspected?.messages).toHaveLength(1);
    expect(state.value.inspected?.session?.title).toBe('Map the parser');
  });

  it('a failed transcript load toasts and stops loading', async () => {
    seed();
    vi.spyOn(api, 'messages').mockRejectedValue(new ApiError(404, 'unknown_session', 'no such session'));
    vi.spyOn(api, 'session').mockRejectedValue(new ApiError(404, 'unknown_session', 'no such session'));
    await actions.inspect('sub-1');
    expect(state.value.inspected?.loading).toBe(false);
    expect(state.value.toasts.at(-1)?.text).toBe('Could not load the agent: no such session');
  });

  it('refreshes the file list once after a burst of file frames', async () => {
    vi.useFakeTimers();
    try {
      seed();
      const files = vi.spyOn(api, 'files').mockResolvedValue([]);
      handleFrames([fx.file, fx.file, fx.file]);
      await vi.advanceTimersByTimeAsync(500);
      expect(files).toHaveBeenCalledTimes(1);
      expect(files).toHaveBeenCalledWith('s1');
      expect(state.value.filesStale).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('messages a teammate from the followed session', async () => {
    seed();
    const post = vi.spyOn(api, 'teamMessage').mockResolvedValue(undefined);
    await expect(actions.messageTeammate('alice', 'rebase first')).resolves.toBeNull();
    expect(post).toHaveBeenCalledWith('alice', 's1', 'rebase first');
  });

  it('returns the gateway message when an interrupt is refused', async () => {
    seed();
    vi.spyOn(api, 'teamInterrupt').mockRejectedValue(new ApiError(409, 'teams_disabled', 'Agent Teams is not enabled'));
    await expect(actions.interruptTeammate('alice')).resolves.toEqual({ tone: 'error', text: 'Agent Teams is not enabled' });
  });
});

// FakeSocket stands in for the browser socket while boot opens the real
// stream over a stubbed fetch.
class FakeSocket {
  static last: FakeSocket | undefined;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  constructor(public url: string, public protocols: string[]) { FakeSocket.last = this; }
  send() {}
  close() {}
}

// stubHealth answers api.health through the real fetch path.
function stubHealth(status: number) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status })));
}

describe('stream and boot hardening', () => {
  beforeEach(() => {
    state.value = initialState();
    FakeSocket.last = undefined;
  });

  it('reloads a followed session as a baseline, not as new activity', async () => {
    seed();
    telemetry.value = emptyTelemetry();
    vi.spyOn(api, 'snapshot').mockResolvedValue({
      ...(fx.snapshot.payload as Snapshot), current_session_id: 's2',
    });
    handleFrames([{ seq: 20, type: 'session_follow', payload: { session_id: 's2' } }]);
    await vi.waitFor(() => expect(state.value.messages.length).toBeGreaterThan(0));
    expect(telemetry.value.tools).toEqual([]);
  });

  it('keeps the query string when it scrubs the token', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    stubHealth(200);
    history.replaceState(null, '', '/?debug=1#t=abc');
    vi.spyOn(api, 'auth').mockResolvedValue(undefined);
    const replace = vi.spyOn(history, 'replaceState');
    try {
      await boot();
      expect(replace).toHaveBeenCalledWith(null, '', '/?debug=1');
    } finally {
      history.replaceState(null, '', '/');
    }
  });

  it('stores the fragment token after the gateway accepts it', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    stubHealth(200);
    history.replaceState(null, '', '/#t=good');
    vi.spyOn(api, 'auth').mockResolvedValue(undefined);
    try {
      await boot();
      expect(token()).toBe('good');
      expect(location.hash).toBe('');
      await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
      expect(FakeSocket.last!.protocols).toEqual(['crush.v1', 'crush.auth.good']);
    } finally {
      history.replaceState(null, '', '/');
    }
  });

  it('drops a fragment token the gateway refuses', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    stubHealth(200);
    history.replaceState(null, '', '/#t=bad');
    vi.spyOn(api, 'auth').mockRejectedValue(new ApiError(401, 'unauthorized', 'invalid token'));
    try {
      await boot();
      expect(token()).toBeNull();
      expect(location.hash).toBe('');
    } finally {
      history.replaceState(null, '', '/');
    }
  });

  it('forgets a stored token from an earlier run', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    stubHealth(401);
    setToken('old');
    await boot();
    await vi.waitFor(() => expect(state.value.conn).toBe('unauthorized'));
    expect(token()).toBeNull();
  });
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('actions.runShell', () => {
  it('starts a session, runs the command and records the run', async () => {
    state.value = { ...initialState(), conn: 'live', hydrated: true, sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const shell = vi.spyOn(api, 'shell').mockResolvedValue({ run_id: 'r1' });

    await expect(actions.runShell('ls -la')).resolves.toBeNull();

    expect(create).toHaveBeenCalledTimes(1);
    expect(shell).toHaveBeenCalledWith('s2', 'ls -la');
    expect(state.value.shells.r1).toMatchObject({
      sessionId: 's2', command: 'ls -la', output: '', done: false, exitCode: null, error: null,
    });
  });

  it('keeps the created session when the command fails, so a retry does not start another', async () => {
    state.value = { ...initialState(), conn: 'live', hydrated: true, sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const shell = vi.spyOn(api, 'shell')
      .mockRejectedValueOnce(new ApiError(500, 'http_error', 'gateway on fire'))
      .mockResolvedValueOnce({ run_id: 'r2' });

    await expect(actions.runShell('ls')).resolves.toEqual({ tone: 'error', text: 'gateway on fire' });
    expect(state.value.sessionId).toBe('s2');

    await expect(actions.runShell('ls')).resolves.toBeNull();
    expect(create).toHaveBeenCalledTimes(1);
    expect(shell).toHaveBeenNthCalledWith(1, 's2', 'ls');
    expect(shell).toHaveBeenNthCalledWith(2, 's2', 'ls');
  });

  it('reports a starting agent as a wait, not an error', async () => {
    seed();
    vi.spyOn(api, 'shell').mockRejectedValue(new ApiError(503, 'agent_starting', 'the agent is starting'));
    await expect(actions.runShell('ls')).resolves.toEqual({ tone: 'starting', text: STARTING_TEXT });
    expect(state.value.shells).toEqual({});
  });
});

describe('actions.addNote', () => {
  it('keeps the note and toasts it, clearing nothing else', async () => {
    seed();
    const note = vi.spyOn(api, 'addNote').mockResolvedValue(undefined);
    await expect(actions.addNote('prefer table tests')).resolves.toBeNull();
    expect(note).toHaveBeenCalledWith('s1', 'prefer table tests');
    expect(state.value.toasts.map((t) => t.text)).toContain('Note kept for the next prompt');
  });

  it('reports a starting agent as a wait, not an error', async () => {
    seed();
    vi.spyOn(api, 'addNote').mockRejectedValue(new ApiError(503, 'agent_starting', 'the agent is starting'));
    await expect(actions.addNote('a note')).resolves.toEqual({ tone: 'starting', text: STARTING_TEXT });
  });

  it('reports a note kept with no session', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: '' };
    const note = vi.spyOn(api, 'addNote');
    const failure = await actions.addNote('a note');
    expect(failure?.tone).toBe('error');
    expect(failure?.text).toContain('no session');
    expect(note).not.toHaveBeenCalled();
  });
});

describe('the side answer', () => {
  it('shows the pending question then the answer', async () => {
    seed();
    const ask = vi.spyOn(api, 'ask').mockResolvedValue({ answer: 'Because the tests say so.' });
    await actions.ask('Why?');
    expect(ask).toHaveBeenCalledWith('s1', 'Why?');
    expect(sideAnswer.value).toEqual({ question: 'Why?', answer: 'Because the tests say so.', error: null });
  });

  it('asks nobody without a session', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: '' };
    const ask = vi.spyOn(api, 'ask');
    await actions.ask('Why?');
    expect(ask).not.toHaveBeenCalled();
    expect(sideAnswer.value?.error?.tone).toBe('error');
  });

  it('shows a starting agent as the starting line', async () => {
    seed();
    vi.spyOn(api, 'ask').mockRejectedValue(new ApiError(503, 'agent_starting', 'the agent is starting'));
    await actions.ask('Why?');
    expect(sideAnswer.value?.error).toEqual({ tone: 'starting', text: STARTING_TEXT });
  });

  it('shows another failure through its message', async () => {
    seed();
    vi.spyOn(api, 'ask').mockRejectedValue(new ApiError(502, 'ask_failed', 'the model did not answer'));
    await actions.ask('Why?');
    expect(sideAnswer.value?.error).toEqual({ tone: 'error', text: 'the model did not answer' });
  });

  it('lets a newer question win over an answer that arrives late', async () => {
    seed();
    const slow = deferred<{ answer: string }>();
    vi.spyOn(api, 'ask')
      .mockReturnValueOnce(slow.promise)
      .mockResolvedValueOnce({ answer: 'newer answer' });

    const first = actions.ask('first');
    const second = actions.ask('second');
    await expect(second).resolves.toBeUndefined();
    slow.resolve({ answer: 'older answer' });
    await expect(first).resolves.toBeUndefined();

    expect(sideAnswer.value).toEqual({ question: 'second', answer: 'newer answer', error: null });
  });

  it('makes a late answer after Dismiss a no-op', async () => {
    seed();
    const slow = deferred<{ answer: string }>();
    vi.spyOn(api, 'ask').mockReturnValue(slow.promise);

    const pending = actions.ask('Why?');
    expect(sideAnswer.value).toEqual({ question: 'Why?', answer: null, error: null });
    dismissSideAnswer();
    expect(sideAnswer.value).toBeNull();

    slow.resolve({ answer: 'late answer' });
    await expect(pending).resolves.toBeUndefined();
    expect(sideAnswer.value).toBeNull();
  });

  it('dismisses the card and makes a late answer a no-op when the tab adopts another session', async () => {
    seed();
    const slow = deferred<{ answer: string }>();
    vi.spyOn(api, 'ask').mockReturnValue(slow.promise);

    const pending = actions.ask('Why?');
    expect(sideAnswer.value?.question).toBe('Why?');

    dispatch((s) => adoptSession(s, 's2'));
    expect(sideAnswer.value).toBeNull();

    slow.resolve({ answer: 'late answer' });
    await expect(pending).resolves.toBeUndefined();
    expect(sideAnswer.value).toBeNull();
  });
});

describe('the document visibility', () => {
  it('tracks the hidden flag on the state', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1' };
    const stop = watchVisibility();
    const setHidden = (hidden: boolean) => {
      Object.defineProperty(document, 'hidden', { configurable: true, value: hidden });
      document.dispatchEvent(new Event('visibilitychange'));
    };
    try {
      setHidden(true);
      expect(state.value.hidden).toBe(true);
      setHidden(false);
      expect(state.value.hidden).toBe(false);
    } finally {
      stop();
      setHidden(false);
    }
  });
});

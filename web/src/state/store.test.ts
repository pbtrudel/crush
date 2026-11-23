import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import { ApiError, api } from '../api/http';
import type { Frame, Message, PermissionRequest, QuestionRequest, Session, Snapshot, WireEvent } from '../api/types';
import { effectsLevel } from './effects';
import { adoptSession, initialState } from './reducer';
import { actions, boot, dispatch, handleFrames, state } from './store';
import { emptyTelemetry, telemetry } from './telemetry';

vi.mock('../api/stream', () => ({ connectStream: vi.fn(() => () => {}) }));

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
});

describe('actions.sendPrompt', () => {
  it('starts a session when the terminal has none', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const send = vi.spyOn(api, 'sendPrompt').mockResolvedValue({ accepted: true });

    await expect(actions.sendPrompt('first task', [])).resolves.toBeNull();

    expect(create).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith('s2', 'first task', []);
  });

  it('sends nothing and reports the failure when the session cannot start', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: '' };
    vi.spyOn(api, 'createSession').mockRejectedValue(new ApiError(500, 'create_session', 'gateway on fire'));
    const send = vi.spyOn(api, 'sendPrompt');

    await expect(actions.sendPrompt('first task', [])).resolves.toBe('Prompt not sent: gateway on fire');

    expect(send).not.toHaveBeenCalled();
  });

  it('keeps the created session when the prompt fails, so a retry does not start another', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: '' };
    const created = { id: 's2', title: 'New Session' } as unknown as Session;
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(created);
    const send = vi.spyOn(api, 'sendPrompt')
      .mockRejectedValueOnce(new ApiError(500, 'http_error', 'gateway on fire'))
      .mockResolvedValueOnce({ accepted: true });

    await expect(actions.sendPrompt('first task', [])).resolves.toBe('Prompt not sent: gateway on fire');
    expect(state.value.sessionId).toBe('s2');

    await expect(actions.sendPrompt('first task', [])).resolves.toBeNull();

    expect(create).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenNthCalledWith(1, 's2', 'first task', []);
    expect(send).toHaveBeenNthCalledWith(2, 's2', 'first task', []);
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
    await expect(actions.interruptTeammate('alice')).resolves.toBe('Agent Teams is not enabled');
  });
});

describe('stream and boot hardening', () => {
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
});

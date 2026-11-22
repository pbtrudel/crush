import { afterEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import { ApiError, api } from '../api/http';
import type { Frame, PermissionRequest, QuestionRequest, Session } from '../api/types';
import { initialState } from './reducer';
import { actions, handleFrames, state } from './store';

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

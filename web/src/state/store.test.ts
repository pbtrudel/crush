import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/http';
import type { PermissionRequest, QuestionRequest } from '../api/types';
import { initialState } from './reducer';
import { actions, state } from './store';

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

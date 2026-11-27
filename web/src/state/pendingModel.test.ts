import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/http';
import { initialState } from './reducer';
import { dispatch, state } from './store';
import {
  pendingModel, pendingReasoning, resetPendingModel, selectModelOrQueue, setReasoningOrQueue,
} from './pendingModel';

function seed(busy: boolean) {
  // Drop the queue before the store moves, so the edge the assignment
  // produces has nothing to flush, then align the watched edge with it.
  resetPendingModel();
  state.value = { ...initialState(), conn: 'live', busy };
  resetPendingModel();
}

// setBusy moves the store the way a frame does, so the busy edge the queue
// watches is the one a turn produces.
function setBusy(busy: boolean) {
  dispatch((s) => ({ ...s, busy }));
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

function toastTexts(): string[] {
  return state.value.toasts.map((t) => t.text);
}

beforeEach(() => {
  vi.restoreAllMocks();
  seed(false);
});

describe('the pending model queue', () => {
  it('queues a model picked while busy without calling the API', async () => {
    setBusy(true);
    const select = vi.spyOn(api, 'selectModel');

    const result = await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');

    expect(result).toBe('queued');
    expect(select).not.toHaveBeenCalled();
    expect(pendingModel.value).toEqual({ type: 'main', provider: 'p1', model: 'm2', name: 'Model Two' });
    expect(toastTexts()).toContain('Large model will switch to Model Two when the turn ends');
    expect(state.value.toasts.at(-1)?.tone).toBe('info');
  });

  it('queues a small model with its own wording', async () => {
    setBusy(true);

    await selectModelOrQueue('small', 'p1', 'm1', 'Model One');

    expect(toastTexts()).toContain('Small model will switch to Model One when the turn ends');
  });

  it('queues a switch the server refuses as busy', async () => {
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(409, 'agent_busy', 'Crush is busy right now'));

    const result = await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');

    expect(result).toBe('queued');
    expect(pendingModel.value?.model).toBe('m2');
  });

  it('rethrows a failure that is not a busy refusal', async () => {
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(400, 'bad_model', 'no such model'));

    await expect(selectModelOrQueue('main', 'p1', 'm2', 'Model Two')).rejects.toThrow('no such model');
    expect(pendingModel.value).toBeNull();
  });

  it('flushes the model then the reasoning on the idle edge', async () => {
    setBusy(true);
    const calls: string[] = [];
    vi.spyOn(api, 'selectModel').mockImplementation(async () => { calls.push('model'); return state.value.model; });
    vi.spyOn(api, 'setReasoning').mockImplementation(async () => { calls.push('reasoning'); return state.value.model; });
    await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');
    await setReasoningOrQueue({ effort: 'high' });

    setBusy(false);
    await settle();

    expect(calls).toEqual(['model', 'reasoning']);
    expect(pendingModel.value).toBeNull();
    expect(pendingReasoning.value).toBeNull();
  });

  it('re-arms a flush the server refuses as busy, waiting for the next edge', async () => {
    setBusy(true);
    const select = vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(409, 'agent_busy', 'Crush is busy right now'));
    await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');

    setBusy(false);
    await settle();

    expect(select).toHaveBeenCalledTimes(1);
    expect(pendingModel.value?.model).toBe('m2');
    await settle();
    expect(select).toHaveBeenCalledTimes(1);

    select.mockResolvedValue(state.value.model);
    setBusy(true);
    setBusy(false);
    await settle();

    expect(select).toHaveBeenCalledTimes(2);
    expect(pendingModel.value).toBeNull();
  });

  it('toasts a flush failure that is not a busy refusal and drops it', async () => {
    setBusy(true);
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(400, 'bad_model', 'no such model'));
    await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');

    setBusy(false);
    await settle();

    expect(pendingModel.value).toBeNull();
    const toast = state.value.toasts.at(-1)!;
    expect(toast.tone).toBe('error');
    expect(toast.text).toBe('no such model');
  });

  it('keeps only the newest selection', async () => {
    setBusy(true);
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');
    await selectModelOrQueue('main', 'p1', 'm3', 'Model Three');

    expect(pendingModel.value?.model).toBe('m3');

    setBusy(false);
    await settle();

    expect(select).toHaveBeenCalledTimes(1);
    expect(select).toHaveBeenCalledWith('main', 'p1', 'm3');
  });

  it('does not flush while the agent is still busy', async () => {
    setBusy(true);
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await selectModelOrQueue('main', 'p1', 'm2', 'Model Two');

    dispatch((s) => ({ ...s, lastSeq: s.lastSeq + 1 }));
    await settle();

    expect(select).not.toHaveBeenCalled();
    expect(pendingModel.value).not.toBeNull();
  });

  it('queues and flushes a thinking toggle', async () => {
    setBusy(true);
    const reasoning = vi.spyOn(api, 'setReasoning').mockResolvedValue(state.value.model);

    const result = await setReasoningOrQueue({ think: true });
    expect(result).toBe('queued');
    expect(reasoning).not.toHaveBeenCalled();
    expect(toastTexts()).toContain('Thinking will turn on when the turn ends');

    setBusy(false);
    await settle();

    expect(reasoning).toHaveBeenCalledWith({ think: true });
    expect(pendingReasoning.value).toBeNull();
  });

  it('names the queued effort in its toast', async () => {
    setBusy(true);

    await setReasoningOrQueue({ effort: 'low' });

    expect(toastTexts()).toContain('Reasoning effort will be set to low when the turn ends');
    expect(pendingReasoning.value).toEqual({ effort: 'low' });
  });

  it('applies at once when the agent is idle', async () => {
    const reasoning = vi.spyOn(api, 'setReasoning').mockResolvedValue(state.value.model);

    const result = await setReasoningOrQueue({ effort: 'high' });

    expect(result).toBe(state.value.model);
    expect(reasoning).toHaveBeenCalledWith({ effort: 'high' });
    expect(pendingReasoning.value).toBeNull();
    expect(toastTexts()).toEqual([]);
  });
});

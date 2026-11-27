import { effect, signal } from '@preact/signals';
import { api, ApiError } from '../api/http';
import type { ModelInfo } from '../api/types';
import { describeFailure } from './failure';
import { pushToast } from './reducer';
import { dispatch, state } from './store';

export type PendingModelType = 'main' | 'small';

export interface PendingModel {
  type: PendingModelType;
  provider: string;
  model: string;
  name: string;
}

export type PendingReasoning = { effort: string } | { think: boolean };

// The selection made mid-turn, held until the agent goes idle: the server
// refuses model and reasoning switches while a turn runs.
export const pendingModel = signal<PendingModel | null>(null);
export const pendingReasoning = signal<PendingReasoning | null>(null);

let flushing = false;
let wasBusy = state.value.busy;

const TYPE_TITLES: Record<PendingModelType, string> = { main: 'Large', small: 'Small' };

function info(text: string) {
  dispatch((s) => pushToast(s, 'info', text));
}

function error(err: unknown) {
  dispatch((s) => pushToast(s, 'error', describeFailure(err).text));
}

function isBusyRefusal(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'agent_busy';
}

function queueModel(selection: PendingModel) {
  pendingModel.value = selection;
  info(`${TYPE_TITLES[selection.type]} model will switch to ${selection.name} when the turn ends`);
}

function queueReasoning(body: PendingReasoning) {
  pendingReasoning.value = body;
  info(reasoningText(body));
}

function reasoningText(body: PendingReasoning): string {
  return 'effort' in body
    ? `Reasoning effort will be set to ${body.effort} when the turn ends`
    : `Thinking will turn ${body.think ? 'on' : 'off'} when the turn ends`;
}

// selectModelOrQueue applies a model choice, or queues it when the agent is
// busy and answers the choice with an info toast.
export async function selectModelOrQueue(
  type: PendingModelType, provider: string, model: string, name: string,
): Promise<'applied' | 'queued'> {
  const selection = { type, provider, model, name };
  if (state.value.busy) {
    queueModel(selection);
    return 'queued';
  }
  try {
    await api.selectModel(type, provider, model);
  } catch (err) {
    if (!isBusyRefusal(err)) throw err;
    queueModel(selection);
    return 'queued';
  }
  return 'applied';
}

// setReasoningOrQueue applies an effort or thinking change, or queues it when
// the agent is busy. An applied change answers the confirmed model info so
// the caller can hold the effort the model took.
export async function setReasoningOrQueue(body: PendingReasoning): Promise<ModelInfo | 'queued'> {
  if (state.value.busy) {
    queueReasoning(body);
    return 'queued';
  }
  try {
    return await api.setReasoning(body);
  } catch (err) {
    if (!isBusyRefusal(err)) throw err;
    queueReasoning(body);
    return 'queued';
  }
}

// flush applies the queued selections, model first: a queued effort belongs
// to the model the user picked with it. A selection is cleared before its
// call so a later edge cannot apply it twice. A busy refusal puts it back and
// waits for the next idle edge; any other failure is reported and dropped.
async function flush(): Promise<void> {
  if (flushing) return;
  flushing = true;
  try {
    const model = pendingModel.value;
    if (model !== null) {
      pendingModel.value = null;
      try {
        await api.selectModel(model.type, model.provider, model.model);
      } catch (err) {
        if (isBusyRefusal(err)) {
          pendingModel.value = model;
          return;
        }
        error(err);
      }
    }
    const reasoning = pendingReasoning.value;
    if (reasoning !== null) {
      pendingReasoning.value = null;
      try {
        await api.setReasoning(reasoning);
      } catch (err) {
        if (isBusyRefusal(err)) {
          pendingReasoning.value = reasoning;
          return;
        }
        error(err);
      }
    }
  } finally {
    flushing = false;
  }
}

// The queue drains on the busy to idle edge only: a refusal means the server
// is still busy, and its next turn supplies the edge that retries.
effect(() => {
  const busy = state.value.busy;
  const idleEdge = wasBusy && !busy;
  wasBusy = busy;
  if (!idleEdge) return;
  if (pendingModel.value === null && pendingReasoning.value === null) return;
  void flush();
});

// Test-only: forgets the queue and the edge it was watching.
export function resetPendingModel(): void {
  pendingModel.value = null;
  pendingReasoning.value = null;
  flushing = false;
  wasBusy = state.value.busy;
}

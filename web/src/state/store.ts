import { signal } from '@preact/signals';
import { api, ApiError, type GrantAction } from '../api/http';
import { connectStream } from '../api/stream';
import type {
  Frame, InputMode, Message, PermissionMode, QuestionAnswer, Snapshot, WireEvent,
} from '../api/types';
import {
  adoptSession, dismissToast, dropQuestion as dropQuestionFrom, initialState, markAnswered, pushToast,
  reduce, setConn, type State,
} from './reducer';
import { effectsLevel, type EffectsLevel } from './effects';
import { observe, telemetry } from './telemetry';

export const state = signal<State>(initialState());

// apply is the only path that moves the state forward, so every change
// feeds the HUD telemetry alongside it.
function apply(fn: (s: State) => State, baseline: boolean) {
  const prev = state.value;
  const next = fn(prev);
  state.value = next;
  telemetry.value = observe(telemetry.value, prev, next, baseline, Date.now());
}

export function dispatch(fn: (s: State) => State) {
  apply(fn, false);
}

function errorText(err: unknown): string {
  return err instanceof ApiError ? err.message : 'The request did not reach crush';
}

// report toasts an error for actions with no control to show it on.
const report = (err: unknown) => dispatch((s) => pushToast(s, 'error', errorText(err)));
const answeredElsewhere = () => dispatch((s) => pushToast(s, 'info', 'Already answered in the terminal'));

// dropPermission removes a request the service no longer holds. The toast
// says why the dialog vanished, the same way an answer from the terminal
// does.
function dropPermission(id: string) {
  answeredElsewhere();
  dispatch((s) => ({ ...s, permissions: s.permissions.filter((p) => p.id !== id) }));
}

// dropQuestion removes a batch the service no longer holds and shows the
// next one waiting, if any.
function dropQuestion(batchId: string) {
  answeredElsewhere();
  dispatch((s) => dropQuestionFrom(s, batchId));
}

// isNotPending reports the gateway error for a request the workspace has
// already resolved.
function isNotPending(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'not_pending';
}

// queueChanged reports the frames after which the queue may have changed.
// The event stream carries no queue events, so the deck asks for the list
// at these points instead.
function queueChanged(f: Frame): boolean {
  if (f.type === 'run_complete') return true;
  if (f.type !== 'message') return false;
  return (f.payload as WireEvent<Message>).payload.role === 'user';
}

// refreshQueue reloads the queue chips. A failure is silent: the chips
// stay as they are and the next snapshot or refresh repairs them.
async function refreshQueue(sid: string) {
  try {
    const { queued } = await api.queue(sid);
    dispatch((s) => (s.sessionId === sid ? { ...s, queued } : s));
  } catch {
    // Nothing to show; the deck already shows the last known queue.
  }
}

async function reloadSession(id: string) {
  try {
    const snap: Snapshot = await api.snapshot(id);
    apply((s) => (s.sessionId === id ? reduce(s, { seq: s.lastSeq, type: 'snapshot', payload: snap }) : s), true);
  } catch (err) {
    dispatch((s) => pushToast(s, 'error', `Could not load the session: ${errorText(err)}`));
  }
}

export async function boot() {
  // The token travels in the fragment so it never reaches server logs. It
  // is exchanged for an HttpOnly cookie and scrubbed from the address bar;
  // it is never logged.
  const token = new URLSearchParams(location.hash.slice(1)).get('t');
  if (token) {
    await api.auth(token).catch(() => undefined);
    history.replaceState(null, '', location.pathname);
  }
  connectStream({
    onFrames: handleFrames,
    onConn: (c) => dispatch((s) => setConn(s, c)),
    lastSeq: () => state.value.lastSeq,
  });
}

// handleFrames applies a batch of stream frames and starts the follow-up
// work the stream cannot express, such as reloading a session or the queue.
// A batch that holds a snapshot is baseline: the history it carries is not
// new activity.
export function handleFrames(frames: Frame[]) {
  apply((s) => frames.reduce(reduce, s), frames.some((f) => f.type === 'snapshot'));
  const id = state.value.needsReload;
  if (id) {
    dispatch((s) => ({ ...s, needsReload: null }));
    void reloadSession(id);
  }
  const sid = state.value.sessionId;
  if (sid && frames.some(queueChanged)) void refreshQueue(sid);
}

export const actions = {
  // sendPrompt returns the error text for the deck to show inline, or null.
  // With no current session the browser starts one the way the terminal
  // does on a first prompt; the gateway then asks the terminal to open it.
  async sendPrompt(prompt: string, files: File[]): Promise<string | null> {
    let sid = state.value.sessionId;
    try {
      if (!sid) {
        sid = (await api.createSession()).id;
        // Own the new session before sending: a failed prompt or a second
        // submit must reuse it instead of starting another. A session_follow
        // for the same id later is a no-op.
        dispatch((s) => adoptSession(s, sid));
      }
      await api.sendPrompt(sid, prompt, files);
      if (state.value.busy) await refreshQueue(sid);
      return null;
    } catch (err) {
      return `Prompt not sent: ${errorText(err)}`;
    }
  },
  async cancel() {
    const sid = state.value.sessionId;
    if (sid) await api.cancel(sid).catch(report);
  },
  async clearQueue() {
    const sid = state.value.sessionId;
    if (!sid) return;
    try {
      await api.clearQueue(sid);
      dispatch((s) => (s.sessionId === sid ? { ...s, queued: [] } : s));
    } catch (err) {
      report(err);
    }
  },
  // grant returns the error text for the dialog to show inline, or null.
  async grant(id: string, action: GrantAction): Promise<string | null> {
    dispatch((s) => markAnswered(s, id));
    try {
      const { resolved } = await api.grant(id, action);
      if (!resolved) dropPermission(id);
      return null;
    } catch (err) {
      if (isNotPending(err)) {
        dropPermission(id);
        return null;
      }
      return errorText(err);
    }
  },
  async setMode(mode: PermissionMode) {
    await api.setMode(mode).catch(report);
  },
  // setInputMode asks the terminal to switch. The frame it publishes back
  // updates the state, so the toggle follows the terminal rather than
  // guessing.
  async setInputMode(mode: InputMode) {
    await api.setInputMode(mode).catch(report);
  },
  // answer returns the error text for the form to show inline, or null.
  async answer(batchId: string, answers: QuestionAnswer[]): Promise<string | null> {
    dispatch((s) => markAnswered(s, batchId));
    try {
      const { resolved } = await api.answer(batchId, answers);
      if (!resolved) dropQuestion(batchId);
      return null;
    } catch (err) {
      if (isNotPending(err)) {
        dropQuestion(batchId);
        return null;
      }
      return errorText(err);
    }
  },
  // cancelQuestion returns the error text for the form to show inline, or
  // null. A batch the terminal already answered is dropped locally, the same
  // way grant treats a permission that is no longer pending.
  async cancelQuestion(batchId: string): Promise<string | null> {
    dispatch((s) => markAnswered(s, batchId));
    try {
      const { resolved } = await api.cancelQuestion(batchId);
      if (!resolved) dropQuestion(batchId);
      return null;
    } catch (err) {
      if (isNotPending(err)) {
        dropQuestion(batchId);
        return null;
      }
      return errorText(err);
    }
  },
  dismissToast: (id: number) => dispatch((s) => dismissToast(s, id)),
  // effectsReduced records a frame-time step down and offers the way back.
  effectsReduced(level: EffectsLevel) {
    effectsLevel.value = level;
    dispatch((s) => pushToast(s, 'warn', 'Reduced effects to keep things smooth', 'restore-effects'));
  },
};

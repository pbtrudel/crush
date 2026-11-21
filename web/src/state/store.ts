import { signal } from '@preact/signals';
import { api, ApiError, type GrantAction } from '../api/http';
import { connectStream } from '../api/stream';
import type { PermissionMode, QuestionAnswer, Snapshot } from '../api/types';
import {
  dismissToast, initialState, markAnswered, pushToast, reduce, setConn, type State,
} from './reducer';

export const state = signal<State>(initialState());

export function dispatch(fn: (s: State) => State) {
  state.value = fn(state.value);
}

function errorText(err: unknown): string {
  return err instanceof ApiError ? err.message : 'The request did not reach crush';
}

export const NO_SESSION_HINT = 'Start or open a session in the terminal';

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

// dropQuestion removes a batch the service no longer holds.
function dropQuestion(batchId: string) {
  answeredElsewhere();
  dispatch((s) => (s.question && s.question.id === batchId ? { ...s, question: null } : s));
}

// isNotPending reports the gateway error for a request the workspace has
// already resolved.
function isNotPending(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'not_pending';
}

async function reloadSession(id: string) {
  try {
    const snap: Snapshot = await api.snapshot(id);
    dispatch((s) => (s.sessionId === id ? reduce(s, { seq: s.lastSeq, type: 'snapshot', payload: snap }) : s));
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
    onFrames: (frames) => {
      dispatch((s) => frames.reduce(reduce, s));
      const id = state.value.needsReload;
      if (id) {
        dispatch((s) => ({ ...s, needsReload: null }));
        void reloadSession(id);
      }
    },
    onConn: (c) => dispatch((s) => setConn(s, c)),
    lastSeq: () => state.value.lastSeq,
  });
}

export const actions = {
  // sendPrompt returns the error text for the deck to show inline, or null.
  // The browser never creates a session in M1: the terminal owns the current
  // session, and prompts for a session the terminal is not viewing would
  // never reach a client the server delivers prompts to.
  async sendPrompt(prompt: string, files: File[]): Promise<string | null> {
    const sid = state.value.sessionId;
    if (!sid) return NO_SESSION_HINT;
    try {
      await api.sendPrompt(sid, prompt, files);
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
    if (sid) await api.clearQueue(sid).catch(report);
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
  // answer returns the error text for the form to show inline, or null.
  async answer(batchId: string, answers: QuestionAnswer[]): Promise<string | null> {
    dispatch((s) => markAnswered(s, batchId));
    try {
      const { resolved } = await api.answer(answers);
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
      const { resolved } = await api.cancelQuestion();
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
};

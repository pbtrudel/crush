import { effect, signal } from '@preact/signals';
import { api, ApiError, type GrantAction } from '../api/http';
import { connectStream } from '../api/stream';
import type {
  Attachment, Frame, InputMode, Message, ModelTarget, PermissionMode, QuestionAnswer, SecurePrompt, SignInState,
  Snapshot, WireEvent,
} from '../api/types';
import {
  adoptSession, applySignIn, applyTerminalSnapshot, clearAwsSso, dismissToast,
  dropQuestion as dropQuestionFrom, initialState, inspect, markAnswered, dropRun, mergeInspected, pushToast,
  reduce, removeShell as removeShellRun, setConn, startShell, unmarkAnswered,
  type State,
} from './reducer';
import { applyBrowse } from './pin';
import { effectsLevel, type EffectsLevel } from './effects';
import { forgetToken, setToken } from './token';
import { describeFailure, type Failure } from './failure';
import { observe, telemetry } from './telemetry';

export const state = signal<State>(initialState());

export interface SideAnswerState {
  question: string;
  answer: string | null;
  error: Failure | null;
}

// sideAnswer is the card above the deck: a question asked beside the
// conversation and the answer to it, which never joins the messages.
export const sideAnswer = signal<SideAnswerState | null>(null);

// A newer question or a Dismiss invalidates the answer still in flight, so
// only the newest ask may reach the card.
let sideAnswerGeneration = 0;

// pinGeneration invalidates a browse or re-pin answer the user has moved
// past: only the newest choice owns the view.
let pinGeneration = 0;

export function dismissSideAnswer(): void {
  resetSideAnswer();
}

// resetSideAnswer drops the card and invalidates the answer in flight.
function resetSideAnswer(): void {
  sideAnswerGeneration++;
  sideAnswer.value = null;
}

// apply is the only path that moves the state forward, so every change
// feeds the HUD telemetry alongside it.
function apply(fn: (s: State) => State, baseline: boolean) {
  const prev = state.value;
  const next = fn(prev);
  // A side answer belongs to the session it was asked in, so adopting
  // another session drops the card and the answer still in flight.
  if (next.sessionId !== prev.sessionId) resetSideAnswer();
  state.value = next;
  telemetry.value = observe(telemetry.value, prev, next, baseline, Date.now());
}

export function dispatch(fn: (s: State) => State) {
  apply(fn, false);
}

function errorText(err: unknown): string {
  return describeFailure(err).text;
}

// BROWSING_REFUSED is why a tab reading another session cannot start work:
// the prompt, command or skill would run in a session the terminal is not
// showing, and the deck is read-only.
const BROWSING_REFUSED = "The tab is browsing another session. Return to the terminal's session to send.";

// browsingFailure reports the refusal while the tab is browsing, or null.
function browsingFailure(): Failure | null {
  return state.value.browsing !== null ? { tone: 'error', text: BROWSING_REFUSED } : null;
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

// EXPIRED_TEXT is what a prompt that left before its answer landed says.
const EXPIRED_TEXT = 'The prompt was already answered or has expired.';

// promptExpired toasts a prompt that left while its answer or cancel was on
// the way. The server refuses those with not_pending, the same error a
// prompt the terminal already answered gives.
const promptExpired = () => dispatch((s) => pushToast(s, 'info', EXPIRED_TEXT));

// queueChanged reports the frames after which the queue may have changed.
// The event stream carries no queue events, so the deck asks for the list
// at these points instead.
function queueChanged(f: Frame): boolean {
  if (f.type === 'run_complete') return true;
  if (f.type !== 'message') return false;
  return (f.payload as WireEvent<Message>).payload.role === 'user';
}

const FILES_DEBOUNCE_MS = 400;
let filesTimer: ReturnType<typeof setTimeout> | null = null;

// filesInFlight and filesDirty keep one list request at a time: a frame that
// lands while the request is on its way asks for exactly one more once it
// settles, however many of them arrive.
let filesInFlight = false;
let filesDirty = false;

async function refreshFiles(sid: string) {
  if (filesInFlight) {
    filesDirty = true;
    return;
  }
  filesInFlight = true;
  try {
    const files = await api.files(sid);
    dispatch((s) => (s.sessionId === sid ? { ...s, files, filesStale: false } : s));
  } catch {
    // The list stays as it is and filesStale stays set, so the next frame or
    // snapshot asks for it again.
  } finally {
    filesInFlight = false;
    if (filesDirty) {
      filesDirty = false;
      const next = state.value.sessionId;
      if (next) void refreshFiles(next);
    }
  }
}

// scheduleFiles coalesces a burst of file frames (a multiedit writes one
// version per edit) into one list request.
function scheduleFiles() {
  if (filesTimer !== null) return;
  filesTimer = setTimeout(() => {
    filesTimer = null;
    const sid = state.value.sessionId;
    if (sid) void refreshFiles(sid);
  }, FILES_DEBOUNCE_MS);
}

// deletedWhileLoading records the messages a frame deletes while a transcript
// is on its way, per session, so the answer cannot bring one back. The set is
// shared by every load of that session in flight: a snapshot that reloads the
// same transcript beside the first one must see the same deletions.
const deletedWhileLoading = new Map<string, Set<string>>();
const inspectedLoads = new Map<string, number>();

async function loadInspected(sid: string) {
  let gone = deletedWhileLoading.get(sid);
  if (gone === undefined) {
    gone = new Set<string>();
    deletedWhileLoading.set(sid, gone);
  }
  inspectedLoads.set(sid, (inspectedLoads.get(sid) ?? 0) + 1);
  try {
    const [messages, session] = await Promise.all([api.messages(sid), api.session(sid).catch(() => null)]);
    dispatch((s) => mergeInspected(s, sid, messages.filter((m) => !gone.has(m.id)), session));
  } catch (err) {
    dispatch((s) => (s.inspected?.sessionId === sid
      ? pushToast({ ...s, inspected: { ...s.inspected, loading: false } }, 'error', `Could not load the agent: ${errorText(err)}`)
      : s));
  } finally {
    const left = (inspectedLoads.get(sid) ?? 1) - 1;
    if (left > 0) {
      inspectedLoads.set(sid, left);
    } else {
      // The last answer settled, so nothing can resurrect those ids any more.
      inspectedLoads.delete(sid);
      deletedWhileLoading.delete(sid);
    }
  }
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
    apply((s) => {
      if (s.sessionId !== id) return s;
      // While browsing, the answer replaces the browsed view through
      // applyBrowse, which never sets needsReload: the reload it answers
      // must not ask for itself again.
      if (s.browsing !== null) return s.browsing === id ? applyBrowse(s, snap) : s;
      return reduce(s, { seq: s.lastSeq, type: 'snapshot', payload: snap });
    }, true);
  } catch (err) {
    dispatch((s) => pushToast(s, 'error', `Could not load the session: ${errorText(err)}`));
  }
}

// watchVisibility keeps state.hidden in step with the tab, so a finished
// turn stays quiet only while the reader is actually looking at it. Returns
// the function that stops watching; boot() watches for the page's lifetime.
export function watchVisibility(): () => void {
  const update = () => dispatch((s) => (s.hidden === document.hidden ? s : { ...s, hidden: document.hidden }));
  document.addEventListener('visibilitychange', update);
  update();
  return () => document.removeEventListener('visibilitychange', update);
}

export async function boot() {
  // The token travels in the fragment so it never reaches server logs.
  // It is checked, kept for this tab only and scrubbed from the address
  // bar; it is never logged.
  const fragment = new URLSearchParams(location.hash.slice(1)).get('t');
  if (fragment) {
    // The token leaves the address bar before the request, not after it.
    history.replaceState(null, '', location.pathname + location.search);
    try {
      await api.auth(fragment);
      setToken(fragment);
    } catch {
      forgetToken();
    }
  }
  watchVisibility();
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
  if (deletedWhileLoading.size > 0) {
    for (const f of frames) {
      if (f.type !== 'message') continue;
      const ev = f.payload as WireEvent<Message>;
      if (ev.type !== 'deleted') continue;
      deletedWhileLoading.get(ev.payload.session_id)?.add(ev.payload.id);
    }
  }
  apply((s) => frames.reduce(reduce, s), frames.some((f) => f.type === 'snapshot'));
  const id = state.value.needsReload;
  if (id) {
    dispatch((s) => ({ ...s, needsReload: null }));
    void reloadSession(id);
  }
  const sid = state.value.sessionId;
  if (sid && frames.some(queueChanged)) void refreshQueue(sid);
  if (state.value.filesStale) scheduleFiles();
  // A snapshot means frames may have been missed, so the inspected child
  // transcript is fetched again.
  const ins = state.value.inspected;
  if (ins && ins.sessionId !== state.value.sessionId && frames.some((f) => f.type === 'snapshot')) void loadInspected(ins.sessionId);
}

// sessionSettled reports whether the tab can tell whether the terminal has
// a session. Only a snapshot settles it while the stream is healthy; a dead
// connection settles it too, so a send fails instead of waiting forever.
function sessionSettled(s: State): boolean {
  return s.hydrated || s.conn === 'ended' || s.conn === 'unauthorized';
}

// SESSION_SNAPSHOT_TIMEOUT_MS bounds how long a send waits for the stream
// to name the tab's session. A socket that accepts and never speaks, or a
// retry loop that never reconnects, must not leave the deck sending
// forever.
const SESSION_SNAPSHOT_TIMEOUT_MS = 10_000;

function whenSessionSettled(): Promise<void> {
  if (sessionSettled(state.value)) return Promise.resolve();
  let done = false;
  let settle: () => void = () => {};
  const settled = new Promise<void>((resolve) => {
    settle = () => {
      if (done) return;
      done = true;
      resolve();
    };
  });
  const dispose = effect(() => {
    if (sessionSettled(state.value)) settle();
  });
  const timer = setTimeout(settle, SESSION_SNAPSHOT_TIMEOUT_MS);
  return settled.finally(() => {
    clearTimeout(timer);
    dispose();
  });
}

// ensureSession returns the session prompts and notes go to, starting one
// the way the terminal does on a first prompt when the browser has none. The
// new session is adopted before anything is sent, so a failed request or a
// second submit reuses it instead of starting another.
async function ensureSession(): Promise<string> {
  let current = state.value.sessionId;
  // A send can land before the first snapshot. The empty sessionId then
  // means the tab does not know the terminal's session yet, not that there
  // is none: creating one here would start a second session and switch the
  // terminal to it. Wait for the snapshot to name the session it shows.
  if (!current && !sessionSettled(state.value)) {
    await whenSessionSettled();
    current = state.value.sessionId;
  }
  if (current) return current;
  // The wait can end without a snapshot (a dead or silent stream). The
  // terminal's session is still unknown then, and creating one would move
  // the terminal as soon as the gateway came back, so fail the send
  // instead.
  if (!state.value.hydrated) throw new Error('the session is unknown');
  const sid = (await api.createSession()).id;
  dispatch((s) => adoptSession(s, sid));
  return sid;
}

export const actions = {
  // sendPrompt returns the failure for the deck to show inline, or null.
  // With no current session the browser starts one the way the terminal
  // does on a first prompt; the gateway then asks the terminal to open it.
  async sendPrompt(prompt: string, files: File[]): Promise<Failure | null> {
    const blocked = browsingFailure();
    if (blocked) return blocked;
    try {
      const sid = await ensureSession();
      await api.sendPrompt(sid, prompt, files);
      if (state.value.busy) await refreshQueue(sid);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // runShell returns the failure for the deck to show inline, or null. A
  // shell command never queues: it runs beside the conversation.
  async runShell(command: string): Promise<Failure | null> {
    const blocked = browsingFailure();
    if (blocked) return blocked;
    try {
      const sid = await ensureSession();
      const { run_id } = await api.shell(sid, command);
      dispatch((s) => (s.sessionId === sid ? startShell(s, run_id, sid, command) : s));
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // addNote keeps text for the session's next prompt. It starts no run, so
  // it is safe while the agent is busy.
  async addNote(text: string): Promise<Failure | null> {
    const sid = state.value.sessionId;
    if (!sid) return { tone: 'error', text: 'There is no session to keep a note in' };
    try {
      await api.addNote(sid, text);
      dispatch((s) => pushToast(s, 'info', 'Note kept for the next prompt'));
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // ask answers a question beside the conversation. The card shows the
  // pending question at once and the answer or the failure when it lands.
  async ask(question: string): Promise<void> {
    const generation = ++sideAnswerGeneration;
    sideAnswer.value = { question, answer: null, error: null };
    const sid = state.value.sessionId;
    if (!sid) {
      sideAnswer.value = { question, answer: null, error: { tone: 'error', text: 'There is no session to ask in' } };
      return;
    }
    try {
      const { answer } = await api.ask(sid, question);
      if (generation !== sideAnswerGeneration) return;
      sideAnswer.value = { question, answer, error: null };
    } catch (err) {
      if (generation !== sideAnswerGeneration) return;
      sideAnswer.value = { question, answer: null, error: describeFailure(err) };
    }
  },
  // removeShell drops one run once its card is done and has had its time on
  // screen.
  removeShell(runId: string) {
    dispatch((s) => removeShellRun(s, runId));
  },
  // dropShell removes one run for good, so its 202 cannot revive the card.
  dropShell(runId: string) {
    dispatch((s) => dropRun(s, runId));
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
  // browse shows another session read-only. The newest choice owns the
  // view, so an answer for a session the user has left is dropped.
  async browse(sid: string): Promise<Failure | null> {
    const generation = ++pinGeneration;
    try {
      const snap = await api.snapshot(sid);
      if (generation !== pinGeneration) return null;
      dispatch((s) => (generation === pinGeneration ? applyBrowse(s, snap) : s));
      return null;
    } catch (err) {
      if (generation !== pinGeneration) return null;
      return describeFailure(err);
    }
  },
  // repin returns to the terminal's session, wherever the terminal is now.
  async repin(): Promise<Failure | null> {
    const generation = ++pinGeneration;
    try {
      const snap = await api.snapshot();
      if (generation !== pinGeneration) return null;
      dispatch((s) => (generation === pinGeneration ? applyTerminalSnapshot(s, snap) : s));
      return null;
    } catch (err) {
      if (generation !== pinGeneration) return null;
      return describeFailure(err);
    }
  },
  // openBrowsedInTerminal asks the terminal to show the browsed session.
  // The session_follow frame it causes pins the tab back to the terminal.
  async openBrowsedInTerminal(): Promise<Failure | null> {
    const sid = state.value.browsing;
    if (!sid) return null;
    try {
      await api.follow(sid);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // runCommand starts a custom command in the session the caller names. A
  // browsing tab refuses, like the prompt path, so hiding the palette rows
  // is not the only barrier.
  async runCommand(sid: string, id: string, args: Record<string, string>): Promise<Failure | null> {
    const blocked = browsingFailure();
    if (blocked) return blocked;
    try {
      await api.runCommand(sid, id, args);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // runMcpPrompt starts an MCP prompt, with the same browsing refusal.
  async runMcpPrompt(
    sid: string, clientId: string, promptId: string, args: Record<string, string>,
  ): Promise<Failure | null> {
    const blocked = browsingFailure();
    if (blocked) return blocked;
    try {
      await api.runMcpPrompt(sid, clientId, promptId, args);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // attachSkill returns the skill's attachment, or the browsing refusal.
  async attachSkill(name: string): Promise<{ attachment: Attachment } | Failure> {
    const blocked = browsingFailure();
    if (blocked) return blocked;
    return api.attachSkill(name);
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
      dispatch((s) => unmarkAnswered(s, id));
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
      dispatch((s) => unmarkAnswered(s, batchId));
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
      dispatch((s) => unmarkAnswered(s, batchId));
      return errorText(err);
    }
  },
  async inspect(sid: string | null) {
    dispatch((s) => inspect(s, sid));
    if (sid && sid !== state.value.sessionId) await loadInspected(sid);
  },
  // answerSecure delivers a secret to the open prompt and returns the
  // failure the dialog shows inline, or null. The secret goes straight into
  // the request body: it is never stored, and a prompt that leaves before
  // the request lands is only toasted, because the terminal answered first.
  async answerSecure(p: SecurePrompt, secret: string): Promise<Failure | null> {
    dispatch((s) => markAnswered(s, p.id));
    try {
      await api.answerSecure(p.source, p.id, secret);
      return null;
    } catch (err) {
      if (isNotPending(err)) {
        promptExpired();
        return null;
      }
      dispatch((s) => unmarkAnswered(s, p.id));
      return describeFailure(err);
    }
  },
  // cancelSecure dismisses an open prompt, with the same rules as an
  // answer: a failure re-arms the dialog so the terminal can still answer.
  async cancelSecure(p: SecurePrompt): Promise<Failure | null> {
    dispatch((s) => markAnswered(s, p.id));
    try {
      await api.cancelSecure(p.source, p.id);
      return null;
    } catch (err) {
      if (isNotPending(err)) {
        promptExpired();
        return null;
      }
      dispatch((s) => unmarkAnswered(s, p.id));
      return describeFailure(err);
    }
  },
  // signInKey saves a provider key and returns what the gateway answered.
  // The key is a local of the call: no state or signal keeps it.
  signInKey: (provider: string, apiKey: string, model?: ModelTarget) =>
    api.signInKey(provider, apiKey, model),
  // signInStart begins one OAuth sign-in and records the challenge it
  // answered with, so the dialog shows it before the frame lands. It answers
  // the state too: a start that finishes at once still belongs to the tab
  // that asked for it.
  async signInStart(provider: string, model?: ModelTarget): Promise<SignInState> {
    const st = await api.signInStart(provider, model);
    dispatch((s) => applySignIn(s, st));
    return st;
  },
  // signInCancel stops a running flow. A flow that already finished answers
  // no_flow, which is not a failure to show.
  async signInCancel(flowId: string): Promise<void> {
    try {
      await api.signInCancel(flowId);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'no_flow') return;
      throw err;
    }
  },
  // signInCode hands back a code pasted from the provider's page. A refused
  // code reaches the dialog as a signin frame, not as this answer.
  signInCode: (flowId: string, code: string) => api.signInCode(flowId, code),
  // dismissAwsSso clears the AWS SSO card; the next refresh opens a new one.
  dismissAwsSso: () => dispatch((s) => clearAwsSso(s)),
  async messageTeammate(member: string, text: string): Promise<Failure | null> {
    const sid = state.value.sessionId;
    if (!sid) return { tone: 'error', text: 'There is no session to message from' };
    try {
      await api.teamMessage(member, sid, text);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  async interruptTeammate(member: string): Promise<Failure | null> {
    const sid = state.value.sessionId;
    if (!sid) return { tone: 'error', text: 'There is no session to interrupt from' };
    try {
      await api.teamInterrupt(member, sid);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  // cancelAgent stops a subagent through its own session, which is how the
  // coordinator reaches a dispatched subagent's run.
  async cancelAgent(sessionId: string): Promise<Failure | null> {
    try {
      await api.cancel(sessionId);
      return null;
    } catch (err) {
      return describeFailure(err);
    }
  },
  dismissToast: (id: number) => dispatch((s) => dismissToast(s, id)),
  // effectsReduced records a frame-time step down and offers the way back.
  effectsReduced(level: EffectsLevel) {
    effectsLevel.value = level;
    dispatch((s) => pushToast(s, 'warn', 'Reduced effects to keep things smooth', 'restore-effects'));
  },
};

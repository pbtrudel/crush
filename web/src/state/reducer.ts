import type {
  AgentEvent, AgentRun, AwsSso, FileSummary, Frame, InputMode, Message, ModelInfo, ModelTarget, Part,
  PermissionMode, PermissionNotification, PermissionRequest, QuestionRequest, SecurePrompt, ServerInfo,
  Session, ShellOutput, SignInState, Snapshot, TeamState, ToolResultData, UpdateAvailable, WebError,
  WireEvent,
} from '../api/types';
import { toastForAgentEvent } from './notifications';

export type Conn = 'booting' | 'live' | 'reconnecting' | 'ended' | 'unauthorized';
export type Tone = 'info' | 'success' | 'warn' | 'error';
export type ToastAction = 'restore-effects' | { kind: 'sign-in'; provider: string; model?: ModelTarget };
export interface Toast { id: number; tone: Tone; text: string; action?: ToastAction }
export interface ServerState { state: string; diagnostics: number; tools: number }
export interface AgentCall { callId: string; tool: string }
export interface Inspected { sessionId: string; session: Session | null; messages: Message[]; loading: boolean }
export interface ShellRun {
  sessionId: string; command: string; output: string; done: boolean; exitCode: number | null;
  error: string | null;
  // persisted marks a run whose result the log already renders. The done
  // frame can beat or follow that message, so the card leaves on whichever
  // of the two arrives last.
  persisted?: boolean;
}

export interface State {
  conn: Conn;
  lastSeq: number;
  // hydrated is true once a snapshot has named the session the terminal
  // shows. Before it, an empty sessionId means "not known yet", not "the
  // terminal has no session". It is deliberately never reset: a resume
  // inside the replay ring sends no snapshot, so clearing it would make a
  // later empty sessionId look unknown again.
  hydrated: boolean;
  version: string;
  workingDir: string;
  project: string;
  branch: string;
  model: ModelInfo;
  agentReady: boolean;
  planReady: string;
  permissionMode: PermissionMode;
  inputMode: InputMode;
  sessionId: string;
  session: Session | null;
  messages: Message[];
  permissions: PermissionRequest[];
  question: QuestionRequest | null;
  questionQueue: QuestionRequest[];
  // securePrompts are the GPG and SSH prompts waiting for an answer. A
  // touch prompt is only a warning toast: the terminal answers it itself.
  securePrompts: SecurePrompt[];
  // answeredHere names the prompt ids this tab answered or cancelled, so
  // the frame that closes them does not toast them back.
  answeredHere: Record<string, true>;
  signin: SignInState | null;
  awsSso: AwsSso | null;
  busy: boolean;
  queued: string[];
  toasts: Toast[];
  nextToastId: number;
  answered: string[];
  lsp: Record<string, ServerState>;
  mcp: Record<string, ServerState>;
  needsReload: string | null;
  files: FileSummary[];
  filesStale: boolean;
  team: TeamState;
  runs: AgentRun[];
  // activity names the tool call a run is on right now. It keeps the last
  // call only, which is what the HUD's phase line shows.
  activity: Record<string, AgentCall>;
  inspected: Inspected | null;
  shells: Record<string, ShellRun>;
  // droppedShells names the runs whose cards already left for good: the 202
  // that starts a run can land after the run's own frames, so its answer
  // must not bring a card back.
  droppedShells: Record<string, true>;
  // hidden mirrors document.hidden, so a finished turn stays quiet only
  // while the reader is actually looking at this tab.
  hidden: boolean;
  // announced remembers the one-shot notices this tab has shown. It
  // survives snapshots and session switches, so a reconnect cannot repeat
  // a stale-config or update toast.
  announced: Record<string, true>;
}

export function initialState(): State {
  return {
    conn: 'booting', lastSeq: 0, hydrated: false, version: '', workingDir: '', project: '', branch: '',
    model: { id: '', name: '', provider: '', context_window: 0, agent_ready: true },
    agentReady: true, planReady: '',
    permissionMode: 'normal', inputMode: 'code', sessionId: '', session: null, messages: [],
    permissions: [], question: null, questionQueue: [], securePrompts: [], answeredHere: {},
    signin: null, awsSso: null, busy: false, queued: [], toasts: [],
    nextToastId: 1, answered: [], lsp: {}, mcp: {}, needsReload: null,
    files: [], filesStale: false, team: emptyTeam(), runs: [], activity: {}, inspected: null, shells: {},
    droppedShells: {}, hidden: false, announced: {},
  };
}

const MAX_TOASTS = 4;

export function pushToast(s: State, tone: Tone, text: string, action?: ToastAction): State {
  const toasts = [...s.toasts, { id: s.nextToastId, tone, text, action }].slice(-MAX_TOASTS);
  return { ...s, toasts, nextToastId: s.nextToastId + 1 };
}

export function dismissToast(s: State, id: number): State {
  return { ...s, toasts: s.toasts.filter((t) => t.id !== id) };
}

export function setConn(s: State, conn: Conn): State {
  return s.conn === conn ? s : { ...s, conn };
}

export function markAnswered(s: State, id: string): State {
  return {
    ...s,
    answered: [...s.answered, id],
    answeredHere: { ...s.answeredHere, [id]: true },
  };
}

// unmarkAnswered undoes markAnswered after a request that never reached
// the workspace, so the terminal's answer still gets its toast.
export function unmarkAnswered(s: State, id: string): State {
  if (!s.answered.includes(id) && !s.answeredHere[id]) return s;
  const answeredHere = { ...s.answeredHere };
  delete answeredHere[id];
  return { ...s, answered: s.answered.filter((x) => x !== id), answeredHere };
}

// enqueueQuestion shows a batch unless one is already on screen, in which
// case it waits: several sessions can each hold a batch at once and the
// form shows one at a time.
export function enqueueQuestion(s: State, q: QuestionRequest): State {
  if (!s.question) return { ...s, question: q };
  if (s.question.id === q.id) return { ...s, question: q };
  const i = s.questionQueue.findIndex((x) => x.id === q.id);
  if (i < 0) return { ...s, questionQueue: [...s.questionQueue, q] };
  const questionQueue = s.questionQueue.slice();
  questionQueue[i] = q;
  return { ...s, questionQueue };
}

// dropQuestion removes a batch the terminal resolved and promotes the next
// waiting one, so a second batch does not stay hidden behind the first.
export function dropQuestion(s: State, id: string): State {
  if (s.question?.id === id) {
    const [next, ...rest] = s.questionQueue;
    return { ...s, question: next ?? null, questionQueue: rest };
  }
  if (!s.questionQueue.some((q) => q.id === id)) return s;
  return { ...s, questionQueue: s.questionQueue.filter((q) => q.id !== id) };
}

function knowsQuestion(s: State, id: string): boolean {
  return s.question?.id === id || s.questionQueue.some((q) => q.id === id);
}

export function adoptSession(s: State, id: string): State {
  return {
    ...s,
    sessionId: id,
    session: null,
    messages: [],
    busy: false,
    needsReload: id,
    files: [],
    filesStale: false,
    activity: {},
    inspected: null,
    shells: {},
    droppedShells: {},
  };
}

export function emptyTeam(): TeamState {
  return { team_id: '', members: [], tasks: [] };
}

// inspect selects the agent the inspector shows. The main session's
// transcript is already in the state, so only a child session loads.
export function inspect(s: State, sid: string | null): State {
  if (!sid) return s.inspected ? { ...s, inspected: null } : s;
  if (s.inspected?.sessionId === sid) return s;
  return { ...s, inspected: { sessionId: sid, session: null, messages: [], loading: sid !== s.sessionId } };
}

// mergeMessages lays the frames that arrived while a transcript loaded over
// the fetched copy: for one id the newer update wins, and a tie goes to the
// live frame.
export function mergeMessages(fetched: Message[], live: Message[]): Message[] {
  let out = fetched;
  for (const m of live) {
    const old = out.find((x) => x.id === m.id);
    if (!old || old.updated_at <= m.updated_at) out = upsertMessage(out, m);
  }
  return out;
}

export function mergeInspected(s: State, sid: string, fetched: Message[], session: Session | null): State {
  const ins = s.inspected;
  if (!ins || ins.sessionId !== sid) return s;
  return {
    ...s,
    inspected: {
      sessionId: sid,
      session: ins.session ?? session,
      messages: mergeMessages(fetched, ins.messages),
      loading: false,
    },
  };
}

export function contextFill(s: State): number {
  const win = s.model.context_window;
  if (!s.session || win <= 0) return 0;
  return Math.min(1, (s.session.prompt_tokens + s.session.completion_tokens) / win);
}

export function needsAttention(s: State): boolean {
  return s.permissions.length > 0 || s.question !== null
    || s.securePrompts.some((p) => p.kind !== 'touch');
}

// planPending reports a ready plan waiting for the reader in the session the
// tab shows. The card is not a dialog: it asks for the core look and the tab
// title, but the log stays interactive.
export function planPending(s: State): boolean {
  return s.planReady !== '' && s.planReady === s.sessionId && s.inputMode === 'plan';
}

// needsYou is what the core look and the tab title call attention for: a
// pending dialog or a ready plan.
export function needsYou(s: State): boolean {
  return needsAttention(s) || planPending(s);
}

function serverMap(m: Record<string, ServerInfo> | undefined): Record<string, ServerState> {
  const out: Record<string, ServerState> = {};
  for (const [name, i] of Object.entries(m ?? {})) {
    out[name] = { state: i.state, diagnostics: i.diagnostic_count ?? 0, tools: i.tool_count ?? 0 };
  }
  return out;
}

export function toolResults(messages: Message[]): Map<string, ToolResultData> {
  const out = new Map<string, ToolResultData>();
  for (const m of messages) {
    for (const p of m.parts) if (p.type === 'tool_result') out.set(p.data.tool_call_id, p.data);
  }
  return out;
}

function projectName(dir: string): string {
  const parts = dir.split(/[\\/]/).filter(Boolean);
  return parts.at(-1) ?? dir;
}

function isOpenAssistant(m: Message): boolean {
  return m.role === 'assistant' && !m.parts.some((p) => p.type === 'finish');
}

// upsertMessage replaces a message with the same id and appends new ones in
// created_at order. A frame that arrives between attach and the snapshot
// build may already be part of the snapshot, so applying it must not
// duplicate the message.
function upsertMessage(list: Message[], m: Message): Message[] {
  const i = list.findIndex((x) => x.id === m.id);
  if (i >= 0) {
    const next = list.slice();
    next[i] = m;
    return next;
  }
  const next = [...list, m];
  if (list.length && list[list.length - 1].created_at > m.created_at) {
    next.sort((a, b) => a.created_at - b.created_at);
  }
  return next;
}

// upsertPermission replaces a request with the same id in place so a
// snapshot and a live frame for the same request never coexist.
function upsertPermission(list: PermissionRequest[], req: PermissionRequest): PermissionRequest[] {
  const i = list.findIndex((p) => p.id === req.id);
  if (i < 0) return [...list, req];
  const next = list.slice();
  next[i] = req;
  return next;
}

function lastToolCall(m: Message): { id: string; name: string } | null {
  for (let i = m.parts.length - 1; i >= 0; i--) {
    const p = m.parts[i];
    if (p.type === 'tool_call') return p.data;
  }
  return null;
}

// keptAnswers prunes the ids this tab answered to the requests a snapshot
// still carries: a snapshot that lands between an answer and its closing
// frame must not forget the tab answered, and the map must not grow across
// sessions.
function keptAnswers(answeredHere: Record<string, true>, snap: Snapshot): Record<string, true> {
  const open = new Set<string>();
  for (const p of snap.secure_prompts ?? []) open.add(p.id);
  for (const p of snap.permissions ?? []) open.add(p.id);
  for (const q of snap.questions ?? []) open.add(q.id);
  const kept: Record<string, true> = {};
  for (const id in answeredHere) {
    if (open.has(id)) kept[id] = true;
  }
  return kept;
}

function applySnapshot(s: State, seq: number, snap: Snapshot): State {
  const messages = snap.messages ?? [];
  const last = messages.at(-1);
  return {
    ...s,
    conn: 'live',
    lastSeq: seq,
    hydrated: true,
    version: snap.version,
    workingDir: snap.working_dir,
    project: projectName(snap.working_dir),
    branch: snap.branch ?? '',
    model: snap.model,
    // The snapshot carries the agent's readiness twice; the top-level field
    // is the same read as model.agent_ready, so either one is authoritative.
    agentReady: snap.agent_ready ?? true,
    planReady: snap.plan_ready_session_id ?? '',
    shells: snap.current_session_id === s.sessionId ? s.shells : {},
    droppedShells: snap.current_session_id === s.sessionId ? s.droppedShells : {},
    permissionMode: snap.permission_mode,
    inputMode: snap.input_mode,
    sessionId: snap.current_session_id,
    session: snap.session ?? null,
    messages,
    permissions: snap.permissions ?? [],
    question: (snap.questions ?? [])[0] ?? null,
    questionQueue: (snap.questions ?? []).slice(1),
    securePrompts: snap.secure_prompts ?? [],
    answeredHere: keptAnswers(s.answeredHere, snap),
    signin: snap.signin ?? null,
    awsSso: snap.aws_sso ? { ...snap.aws_sso } : null,
    busy: snap.busy || (last ? isOpenAssistant(last) : false),
    queued: snap.queued ?? [],
    lsp: serverMap(snap.lsp),
    mcp: serverMap(snap.mcp),
    needsReload: null,
    files: snap.files ?? [],
    filesStale: false,
    team: snap.team ?? emptyTeam(),
    runs: snap.agent_runs ?? [],
  };
}

// SHELL_CAP bounds one shell run's retained output. A command that prints
// without end would otherwise grow the tab's memory with every chunk.
const SHELL_CAP = 256 * 1024;

// startShell records a run the tab just started. The gateway starts the
// command before it answers with the run id, so frames can create the run
// first; the start then only adds the command and the session and keeps the
// output and the outcome those frames already carried.
export function startShell(s: State, runId: string, sessionId: string, command: string): State {
  // A fast command can finish before its 202 names the run. The frames
  // already left, and the card must not come back without output.
  if (s.droppedShells[runId]) return s;
  const prev = s.shells[runId];
  if (prev?.done && prev.persisted) return s;
  const run: ShellRun = prev
    ? { ...prev, sessionId, command }
    : { sessionId, command, output: '', done: false, exitCode: null, error: null };
  return { ...s, shells: { ...s.shells, [runId]: run } };
}

// removeShell drops one run, which the card does once the run has finished
// and its persisted message has had time to arrive.
export function removeShell(s: State, runId: string): State {
  if (!(runId in s.shells)) return s;
  const shells = { ...s.shells };
  delete shells[runId];
  return { ...s, shells };
}

// dropRun removes one run and remembers it, so the 202 that names it cannot
// bring a card back after the frames have already left.
export function dropRun(s: State, runId: string): State {
  const shells = { ...s.shells };
  delete shells[runId];
  return { ...s, shells, droppedShells: { ...s.droppedShells, [runId]: true } };
}

// capShell keeps the last SHELL_CAP characters and starts the kept text on
// a line boundary, so a trimmed run never opens mid-line. A tail that is
// one line, or one unfinished line, keeps all of itself: dropping the line
// there would leave nothing to show.
function capShell(output: string): string {
  if (output.length <= SHELL_CAP) return output;
  const tail = output.slice(-SHELL_CAP);
  const nl = tail.indexOf('\n');
  return nl < 0 || nl === tail.length - 1 ? tail : tail.slice(nl + 1);
}

// applyShellOutput appends a chunk to its run, creating the run for a frame
// from another tab (which carries no command), and settles it on the done
// frame.
function applyShellOutput(s: State, e: ShellOutput): State {
  // A run that already left for good ignores every frame, so a late chunk or
  // a replayed done cannot put an empty card back.
  if (s.droppedShells[e.run_id]) return s;
  // The message that persists the result can arrive before the done frame.
  // Once both are in, the log's own rendering has the card's place.
  if (e.done && s.shells[e.run_id]?.persisted) return dropRun(s, e.run_id);
  const prev = s.shells[e.run_id] ?? {
    sessionId: e.session_id, command: '', output: '', done: false, exitCode: null, error: null,
  };
  const run: ShellRun = {
    ...prev,
    output: capShell(prev.output + (e.chunk ?? '')),
    done: prev.done || !!e.done,
    exitCode: e.done ? e.exit_code ?? null : prev.exitCode,
    error: e.done ? e.error ?? null : prev.error,
  };
  return { ...s, shells: { ...s.shells, [e.run_id]: run } };
}

// dropPersistedShells removes the live cards a persisted shell message now
// renders: one done run per shell_command part, oldest first, so a card
// never lingers twice beside the log's own rendering.
function dropPersistedShells(s: State, m: Message): State {
  const commands = m.parts
    .filter((p): p is Extract<Part, { type: 'shell_command' }> => p.type === 'shell_command')
    .map((p) => p.data.command);
  if (commands.length === 0) return s;
  const shells = { ...s.shells };
  const dropped: Record<string, true> = {};
  const claimable = (id: string) => shells[id].sessionId === m.session_id
    && (shells[id].done || !shells[id].persisted);
  for (const command of commands) {
    // Another tab's run carries no command, and the message's command is
    // then the only thing that names it.
    const id = Object.keys(shells).find((x) => claimable(x) && shells[x].command === command)
      ?? Object.keys(shells).find((x) => claimable(x) && shells[x].command === '');
    if (id === undefined) continue;
    if (shells[id].done) {
      delete shells[id];
      dropped[id] = true;
    } else {
      shells[id] = { ...shells[id], persisted: true };
    }
  }
  if (Object.keys(dropped).length === 0) return { ...s, shells };
  return { ...s, shells, droppedShells: { ...s.droppedShells, ...dropped } };
}

// applyAgentEvent folds the AWS SSO refresh frames into the card. The
// command replaces, the URL fills in, and a result clears the card unless
// it carries the error the card stays up to show.
function applyAgentEvent(s: State, e: AgentEvent): State {
  if (e.type === 'aws_sso_auth') {
    if (!e.aws_sso_command && !e.aws_sso_url) return s;
    const card = s.awsSso;
    // A new command, or anything that follows a failed refresh, starts a
    // fresh card: a stale verification URL must not come back.
    if (!card || card.error !== undefined || (e.aws_sso_command && !e.aws_sso_url)) {
      return {
        ...s,
        awsSso: {
          command: e.aws_sso_command || '',
          ...(e.aws_sso_url ? { url: e.aws_sso_url } : {}),
        },
      };
    }
    return {
      ...s,
      awsSso: {
        command: e.aws_sso_command || card.command,
        ...(e.aws_sso_url || card.url ? { url: e.aws_sso_url || card.url } : {}),
      },
    };
  }
  if (e.type === 'aws_sso_auth_result') {
    if (!e.error) return { ...s, awsSso: null };
    return { ...s, awsSso: { ...s.awsSso, command: s.awsSso?.command ?? '', error: e.error } };
  }
  return s;
}

// clearAwsSso dismisses the AWS SSO card. A later command starts a fresh
// card, as the next refresh does.
export function clearAwsSso(s: State): State {
  return s.awsSso === null ? s : { ...s, awsSso: null };
}

// signInFinished reports whether a phase ends the flow.
function signInFinished(state: string): boolean {
  return state === 'done' || state === 'error' || state === 'cancelled';
}

// applySignIn records the sign-in the gateway named and announces a
// finished one this tab has not shown yet. A start applies its answer here
// so the panel shows the challenge without waiting for the frame.
export function applySignIn(s: State, st: SignInState): State {
  const current = s.signin;
  // The token frame can beat the start's own answer. A waiting state for a
  // flow that already finished is that late answer and must not reopen it.
  if (current && current.flow_id === st.flow_id && signInFinished(current.state) && !signInFinished(st.state)) {
    return s;
  }
  const announced = current?.flow_id === st.flow_id && current.state === 'done';
  const out = { ...s, signin: st };
  return st.state === 'done' && !announced
    ? pushToast(out, 'info', `Signed in to ${st.provider_name}.`)
    : out;
}

export function reduce(s: State, f: Frame): State {
  const next = { ...s, lastSeq: Math.max(s.lastSeq, f.seq) };
  const ev = f.payload as WireEvent<any>;
  switch (f.type) {
    case 'snapshot':
      return applySnapshot(s, f.seq, f.payload as Snapshot);
    case 'session_follow': {
      const id = (f.payload as { session_id: string }).session_id;
      return id === s.sessionId ? next : { ...adoptSession(next, id), needsReload: id || null };
    }
    case 'input_mode_changed':
      return { ...next, inputMode: (f.payload as { mode: InputMode }).mode };
    case 'gateway_closing':
      return { ...next, conn: 'ended', busy: false };
    case 'web_error': {
      const e = f.payload as WebError;
      // A replay after a reconnect repeats a seq the state already holds;
      // only a new frame is a new error.
      if (f.seq <= s.lastSeq) return next;
      return pushToast(next, e.code === 'agent_starting' ? 'warn' : 'error', e.error);
    }
    case 'model_changed': {
      const m = f.payload as ModelInfo;
      return { ...next, model: m, agentReady: m.agent_ready };
    }
    case 'shell_output':
      return applyShellOutput(next, f.payload as ShellOutput);
    case 'plan_ready':
      return { ...next, planReady: (f.payload as { session_id: string }).session_id };
    case 'agent_event': {
      // A replay after a reconnect repeats a seq the state already has;
      // only a new frame is news.
      if (f.seq <= s.lastSeq) return next;
      const e = (ev.payload ?? {}) as AgentEvent;
      const out = applyAgentEvent(next, e);
      const toast = toastForAgentEvent(e, {
        currentSession: s.sessionId,
        hidden: s.hidden,
        model: { provider: s.model.provider, id: s.model.id },
      });
      return toast ? pushToast(out, toast.tone, toast.text, toast.action) : out;
    }
    case 'config_stale': {
      if (f.seq <= s.lastSeq || s.announced.config_stale) return next;
      return pushToast(
        { ...next, announced: { ...s.announced, config_stale: true } },
        'info',
        'Configuration changed on disk. Reload it from the palette.',
      );
    }
    case 'update_available': {
      if (f.seq <= s.lastSeq || s.announced.update_available) return next;
      // A frame without a version carries nothing to announce, and one
      // without a current version cannot name the upgrade.
      const u = (ev?.payload ?? {}) as Partial<UpdateAvailable>;
      if (!u.latest_version) return next;
      if (!u.is_development && !u.current_version) return next;
      const text = u.is_development
        ? `This is a development version of Crush. The latest version is v${u.latest_version}.`
        : `Crush update available: v${u.current_version} → v${u.latest_version}.`;
      return pushToast({ ...next, announced: { ...s.announced, update_available: true } }, 'info', text);
    }
    case 'message': {
      const m = ev.payload as Message;
      let base: State = next;
      if (m.session_id !== s.sessionId && m.role === 'assistant') {
        const call = lastToolCall(m);
        if (call && s.activity[m.session_id]?.callId !== call.id) {
          base = { ...base, activity: { ...s.activity, [m.session_id]: { callId: call.id, tool: call.name } } };
        }
      }
      const ins = s.inspected;
      if (ins && m.session_id === ins.sessionId && m.session_id !== s.sessionId) {
        const messages = ev.type === 'deleted' ? ins.messages.filter((x) => x.id !== m.id) : upsertMessage(ins.messages, m);
        base = { ...base, inspected: { ...ins, messages } };
      }
      if (m.session_id !== s.sessionId) return base;
      if (ev.type === 'deleted') return { ...base, messages: s.messages.filter((x) => x.id !== m.id) };
      base = dropPersistedShells(base, m);
      const messages = upsertMessage(s.messages, m);
      // A bang command's result is a user message, but it starts no agent
      // turn, so it leaves the session as busy as it found it.
      const shellResult = m.parts.some((p) => p.type === 'shell_command');
      const busy = m.role === 'assistant'
        ? isOpenAssistant(m)
        : shellResult ? s.busy : s.busy || m.role === 'user';
      return { ...base, messages, busy };
    }
    case 'session': {
      const sess = ev.payload as Session;
      let base = next;
      if (s.inspected && s.inspected.sessionId === sess.id) {
        base = { ...base, inspected: { ...s.inspected, session: sess } };
      }
      if (sess.id !== s.sessionId) return base;
      return { ...base, session: sess, busy: sess.is_busy || s.busy };
    }
    case 'run_complete': {
      const rc = ev.payload as { session_id: string };
      return rc.session_id === s.sessionId ? { ...next, busy: false } : next;
    }
    case 'secure_prompt': {
      const prompts = (f.payload as { prompts?: SecurePrompt[] }).prompts ?? [];
      const listed = { ...next, securePrompts: prompts };
      // A replay after a reconnect still replaces the list, because the
      // frame is the whole set, but it is not news.
      if (f.seq <= s.lastSeq) return listed;
      const open = new Set(prompts.map((p) => p.id));
      const answeredHere = { ...s.answeredHere };
      let goneElsewhere = false;
      for (const gone of s.securePrompts) {
        if (open.has(gone.id)) continue;
        const answered = answeredHere[gone.id];
        delete answeredHere[gone.id];
        // The terminal answers every touch prompt itself, so a touch
        // leaving the list is no news either.
        if (!answered && gone.kind !== 'touch') goneElsewhere = true;
      }
      // One frame can close several dialogs; one notice covers them all.
      let out = goneElsewhere ? pushToast(listed, 'info', 'Answered in the terminal') : listed;
      const before = new Set(s.securePrompts.map((p) => p.id));
      for (const p of prompts) {
        if (p.kind === 'touch' && !before.has(p.id)) out = pushToast(out, 'warn', p.message);
      }
      return { ...out, answeredHere };
    }
    case 'signin': {
      // A replay after a reconnect repeats a seq the state already has;
      // only a new frame is news. The server drops frames from a replaced
      // run, so no flow_id check is needed here.
      if (f.seq <= s.lastSeq) return next;
      return applySignIn(next, f.payload as SignInState);
    }
    case 'permission_request':
      return { ...next, permissions: upsertPermission(s.permissions, ev.payload as PermissionRequest) };
    case 'permission_notification': {
      const n = ev.payload as PermissionNotification;
      const gone = s.permissions.filter((p) => p.tool_call_id === n.tool_call_id);
      if (!gone.length) return next;
      const out = { ...next, permissions: s.permissions.filter((p) => p.tool_call_id !== n.tool_call_id) };
      const local = gone.some((p) => s.answered.includes(p.id));
      return local || n.cancelled ? out : pushToast(out, 'info', 'Permission answered in the terminal');
    }
    case 'question_batch_request':
      return enqueueQuestion(next, ev.payload as QuestionRequest);
    case 'question_batch_notification': {
      const n = ev.payload as { batch_id: string };
      if (!knowsQuestion(s, n.batch_id)) return next;
      const out = dropQuestion(next, n.batch_id);
      return s.answered.includes(n.batch_id) ? out : pushToast(out, 'info', 'Question answered in the terminal');
    }
    case 'permission_mode_changed':
      return { ...next, permissionMode: (ev.payload as { mode: PermissionMode }).mode };
    case 'lsp_event': {
      // LSP states are the numeric lsp.ServerState as a string in snapshots
      // and a bare number in live frames, so both are stringified here.
      const e = ev.payload as { name: string; state: string | number; diagnostic_count?: number };
      return { ...next, lsp: { ...s.lsp, [e.name]: { state: String(e.state), diagnostics: e.diagnostic_count ?? 0, tools: 0 } } };
    }
    case 'mcp_event': {
      const e = ev.payload as { name: string; state: string; tool_count?: number };
      if (!e.name) return next;
      return { ...next, mcp: { ...s.mcp, [e.name]: { state: String(e.state), diagnostics: 0, tools: e.tool_count ?? 0 } } };
    }
    case 'team_state':
      return { ...next, team: f.payload as TeamState };
    case 'agent_runs':
      return { ...next, runs: f.payload as AgentRun[] };
    case 'file':
      return next.filesStale ? next : { ...next, filesStale: true };
    default:
      return next;
  }
}

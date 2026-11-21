import type {
  Frame, Message, ModelInfo, PermissionMode, PermissionNotification, PermissionRequest,
  QuestionRequest, ServerInfo, Session, Snapshot, ToolResultData, WireEvent,
} from '../api/types';

export type Conn = 'booting' | 'live' | 'reconnecting' | 'ended' | 'unauthorized';
export type Tone = 'info' | 'success' | 'warn' | 'error';
export interface Toast { id: number; tone: Tone; text: string }
export interface ServerState { state: string; diagnostics: number; tools: number }

export interface State {
  conn: Conn;
  lastSeq: number;
  version: string;
  workingDir: string;
  project: string;
  branch: string;
  model: ModelInfo;
  permissionMode: PermissionMode;
  sessionId: string;
  session: Session | null;
  messages: Message[];
  permissions: PermissionRequest[];
  question: QuestionRequest | null;
  busy: boolean;
  queued: string[];
  toasts: Toast[];
  nextToastId: number;
  answered: string[];
  lsp: Record<string, ServerState>;
  mcp: Record<string, ServerState>;
  needsReload: string | null;
}

export function initialState(): State {
  return {
    conn: 'booting', lastSeq: 0, version: '', workingDir: '', project: '', branch: '',
    model: { id: '', name: '', provider: '', context_window: 0 },
    permissionMode: 'normal', sessionId: '', session: null, messages: [],
    permissions: [], question: null, busy: false, queued: [], toasts: [],
    nextToastId: 1, answered: [], lsp: {}, mcp: {}, needsReload: null,
  };
}

const MAX_TOASTS = 4;

export function pushToast(s: State, tone: Tone, text: string): State {
  const toasts = [...s.toasts, { id: s.nextToastId, tone, text }].slice(-MAX_TOASTS);
  return { ...s, toasts, nextToastId: s.nextToastId + 1 };
}

export function dismissToast(s: State, id: number): State {
  return { ...s, toasts: s.toasts.filter((t) => t.id !== id) };
}

export function setConn(s: State, conn: Conn): State {
  return s.conn === conn ? s : { ...s, conn };
}

export function markAnswered(s: State, id: string): State {
  return { ...s, answered: [...s.answered, id] };
}

export function adoptSession(s: State, id: string): State {
  return { ...s, sessionId: id, session: null, messages: [], busy: false, needsReload: id };
}

export function contextFill(s: State): number {
  const win = s.model.context_window;
  if (!s.session || win <= 0) return 0;
  return Math.min(1, (s.session.prompt_tokens + s.session.completion_tokens) / win);
}

export function needsAttention(s: State): boolean {
  return s.permissions.length > 0 || s.question !== null;
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

function applySnapshot(s: State, seq: number, snap: Snapshot): State {
  const messages = snap.messages ?? [];
  const last = messages.at(-1);
  return {
    ...s,
    conn: 'live',
    lastSeq: seq,
    version: snap.version,
    workingDir: snap.working_dir,
    project: projectName(snap.working_dir),
    branch: snap.branch ?? '',
    model: snap.model,
    permissionMode: snap.permission_mode,
    sessionId: snap.current_session_id,
    session: snap.session ?? null,
    messages,
    permissions: snap.permissions ?? [],
    question: snap.question ?? null,
    busy: snap.busy || (last ? isOpenAssistant(last) : false),
    queued: snap.queued ?? [],
    lsp: serverMap(snap.lsp),
    mcp: serverMap(snap.mcp),
    needsReload: null,
  };
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
    case 'gateway_closing':
      return { ...next, conn: 'ended', busy: false };
    case 'web_error':
      return pushToast(next, 'error', (f.payload as { error: string }).error);
    case 'message': {
      const m = ev.payload as Message;
      if (m.session_id !== s.sessionId) return next;
      if (ev.type === 'deleted') return { ...next, messages: s.messages.filter((x) => x.id !== m.id) };
      const messages = upsertMessage(s.messages, m);
      const busy = m.role === 'assistant' ? isOpenAssistant(m) : s.busy || m.role === 'user';
      return { ...next, messages, busy };
    }
    case 'session': {
      const sess = ev.payload as Session;
      if (sess.id !== s.sessionId) return next;
      return { ...next, session: sess, busy: sess.is_busy || s.busy };
    }
    case 'run_complete': {
      const rc = ev.payload as { session_id: string };
      return rc.session_id === s.sessionId ? { ...next, busy: false } : next;
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
      return { ...next, question: ev.payload as QuestionRequest };
    case 'question_batch_notification': {
      const n = ev.payload as { batch_id: string };
      if (!s.question || s.question.id !== n.batch_id) return next;
      const out = { ...next, question: null };
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
    default:
      return next;
  }
}

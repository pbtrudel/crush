import type {
  Attachment, CommandEntry, DiagnosticCounts, FileDiff, FileSummary, InputMode, LspServer, McpPrompt,
  McpServer, Message, ModelChoice, ModelInfo, PermissionMode, QuestionAnswer, Session, SkillEntry, Snapshot,
} from './types';

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', headers: {} };
  if (body instanceof FormData) init.body = body;
  else if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
  }
  const rsp = await fetch(path, init);
  if (rsp.status === 204) return undefined as T;
  const data = await rsp.json().catch(() => ({}));
  if (!rsp.ok) throw new ApiError(rsp.status, data.code ?? 'http_error', data.error ?? rsp.statusText);
  return data as T;
}

export type GrantAction = 'allow' | 'allow_session' | 'deny';

export const api = {
  auth: (token: string) => call<void>('POST', '/api/auth', { token }),
  async health(): Promise<'ok' | 'unauthorized' | 'down'> {
    try {
      const rsp = await fetch('/api/health', { credentials: 'same-origin' });
      if (rsp.status === 401) return 'unauthorized';
      return rsp.ok ? 'ok' : 'down';
    } catch {
      return 'down';
    }
  },
  snapshot: (sid?: string) => call<Snapshot>('GET', `/api/snapshot${sid ? `?session=${encodeURIComponent(sid)}` : ''}`),
  createSession: (title?: string) => call<Session>('POST', '/api/sessions', title ? { title } : {}),
  messages: (sid: string) => call<Message[]>('GET', `/api/sessions/${encodeURIComponent(sid)}/messages`),
  sendPrompt(sid: string, prompt: string, files: File[]) {
    const form = new FormData();
    form.set('prompt', prompt);
    for (const f of files) form.append('attachments', f, f.name);
    return call<{ accepted: boolean }>('POST', `/api/sessions/${encodeURIComponent(sid)}/prompt`, form);
  },
  cancel: (sid: string) => call<void>('POST', `/api/sessions/${encodeURIComponent(sid)}/cancel`),
  queue: (sid: string) => call<{ queued: string[] }>('GET', `/api/sessions/${encodeURIComponent(sid)}/queue`),
  clearQueue: (sid: string) => call<void>('DELETE', `/api/sessions/${encodeURIComponent(sid)}/queue`),
  grant: (id: string, action: GrantAction) => call<{ resolved: boolean }>('POST', '/api/permissions/grant', { id, action }),
  setMode: (mode: PermissionMode) => call<void>('POST', '/api/permissions/mode', { mode }),
  setInputMode: (mode: InputMode) => call<void>('POST', '/api/input-mode', { mode }),
  answer: (batchId: string, answers: QuestionAnswer[]) => call<{ resolved: boolean }>('POST', '/api/questions/answer', { batch_id: batchId, answers }),
  cancelQuestion: (batchId: string) => call<{ resolved: boolean }>('POST', '/api/questions/cancel', { batch_id: batchId }),
  completeFiles: (q: string) => call<{ files: string[]; truncated: boolean }>('GET', `/api/complete/files?q=${encodeURIComponent(q)}`),
  session: (sid: string) => call<Session>('GET', `/api/sessions/${encodeURIComponent(sid)}`),
  files: (sid: string) => call<FileSummary[]>('GET', `/api/sessions/${encodeURIComponent(sid)}/files`),
  diff(sid: string, path: string, from?: number, to?: number) {
    const q = new URLSearchParams({ path });
    if (from !== undefined) q.set('from', String(from));
    if (to !== undefined) q.set('to', String(to));
    return call<FileDiff>('GET', `/api/sessions/${encodeURIComponent(sid)}/files/diff?${q}`);
  },
  teamMessage: (member: string, sessionId: string, text: string) =>
    call<void>('POST', `/api/team/members/${encodeURIComponent(member)}/message`, { session_id: sessionId, text }),
  teamInterrupt: (member: string, sessionId: string) =>
    call<void>('POST', `/api/team/members/${encodeURIComponent(member)}/interrupt`, { session_id: sessionId }),
  reloadConfig: () => call<void>('POST', '/api/config/reload'),
  renameSession: (sid: string, title: string) =>
    call<Session>('PUT', `/api/sessions/${encodeURIComponent(sid)}`, { title }),
  deleteSession: (sid: string) => call<void>('DELETE', `/api/sessions/${encodeURIComponent(sid)}`),
  follow: (sid: string) => call<void>('POST', '/api/follow', { session_id: sid }),
  sessions: () => call<Session[]>('GET', '/api/sessions'),
  commands: () => call<{ commands: CommandEntry[] }>('GET', '/api/commands'),
  runCommand: (sid: string, id: string, args: Record<string, string>) =>
    call<void>('POST', '/api/commands/run', { session_id: sid, id, args }),
  attachSkill: (name: string) =>
    call<{ attachment: Attachment }>('POST', `/api/skills/${encodeURIComponent(name)}/attach`),
  setSkillDisabled: (name: string, disabled: boolean) =>
    call<void>('POST', `/api/skills/${encodeURIComponent(name)}/${disabled ? 'disable' : 'enable'}`),
  mcpPrompts: () => call<{ prompts: McpPrompt[] }>('GET', '/api/mcp/prompts'),
  runMcpPrompt: (sid: string, clientId: string, promptId: string, args: Record<string, string>) =>
    call<void>('POST', '/api/mcp/prompts/run', { session_id: sid, client_id: clientId, prompt_id: promptId, args }),
  skills: () => call<{ skills: SkillEntry[] }>('GET', '/api/skills'),
  reloadSkills: () => call<void>('POST', '/api/skills/reload'),
  summarize: (sid: string) => call<void>('POST', `/api/sessions/${encodeURIComponent(sid)}/summarize`),
  ask: (sid: string, question: string) =>
    call<{ answer: string }>('POST', `/api/sessions/${encodeURIComponent(sid)}/ask`, { question }),
  shell: (sid: string, command: string) =>
    call<{ run_id: string }>('POST', `/api/sessions/${encodeURIComponent(sid)}/shell`, { command }),
  cancelShell: (runId: string) => call<void>('DELETE', `/api/shell/${encodeURIComponent(runId)}`),
  addNote: (sid: string, text: string) =>
    call<void>('POST', `/api/sessions/${encodeURIComponent(sid)}/notes`, { text }),
  providers: () => call<{ choices: ModelChoice[] }>('GET', '/api/providers'),
  selectModel: (type: 'main' | 'small', provider: string, model: string) =>
    call<ModelInfo>('POST', '/api/models/select', { type, provider, model }),
  setReasoning: (body: { effort: string } | { think: boolean }) =>
    call<ModelInfo>('POST', '/api/models/reasoning', body),
  mcpServers: () => call<{ servers: McpServer[] }>('GET', '/api/mcp'),
  mcpAction: (name: string, action: 'start' | 'refresh' | 'enable' | 'disable') =>
    call<void>('POST', `/api/mcp/${encodeURIComponent(name)}/${action}`),
  mcpAuth: (name: string) => call<{ url: string }>('POST', `/api/mcp/${encodeURIComponent(name)}/auth`),
  lspServers: () => call<{ servers: LspServer[] }>('GET', '/api/lsp'),
  lspDiagnostics: (name: string) =>
    call<DiagnosticCounts>('GET', `/api/lsp/${encodeURIComponent(name)}/diagnostics`),
  lspStart: (path?: string) => call<void>('POST', '/api/lsp/start', path ? { path } : {}),
  lspStop: () => call<void>('POST', '/api/lsp/stop'),
  planHandoff: (sid: string, action: 'code' | 'code_yolo') =>
    call<void>('POST', '/api/plan/handoff', { session_id: sid, action }),
};

import type {
  Attachment, CommandEntry, ConfigView, DiagnosticCounts, DisplaySettings, DockerMcp, FileDiff, FileSummary, InputMode,
  LspServer, McpPrompt, McpServer, Message, ModelChoice, ModelInfo, ModelTarget, PermissionMode, QuestionAnswer,
  Session, SignInResult, SignInState, SignInTarget, SkillEntry, Snapshot, SecurePrompt,
} from './types';
import { forgetToken, token } from '../state/token';

// authHeaders carries the tab's token on every API call. The cookie is
// gone: cookies ignore ports, so another 127.0.0.1 server would see it.
export function authHeaders(): Record<string, string> {
  const t = token();
  return t ? { Authorization: 'Bearer ' + t } : {};
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'omit', headers: { ...authHeaders() } };
  if (body instanceof FormData) init.body = body;
  else if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
  }
  const rsp = await fetch(path, init);
  if (rsp.status === 204) return undefined as T;
  const data = await rsp.json().catch(() => ({}));
  if (!rsp.ok) {
    if (rsp.status === 401) forgetToken();
    throw new ApiError(rsp.status, data.code ?? 'http_error', data.error ?? rsp.statusText);
  }
  return data as T;
}

export type GrantAction = 'allow' | 'allow_session' | 'deny';

export const api = {
  auth: (token: string) => call<void>('POST', '/api/auth', { token }),
  async health(): Promise<'ok' | 'unauthorized' | 'down'> {
    try {
      const rsp = await fetch('/api/health', { credentials: 'omit', headers: authHeaders() });
      if (rsp.status === 401) {
        forgetToken();
        return 'unauthorized';
      }
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
  setAgentTeams: (enabled: boolean) => call<void>('POST', '/api/agent-teams', { enabled }),
  dockerMcp: (enable: boolean) =>
    call<void>('POST', `/api/docker-mcp/${enable ? 'enable' : 'disable'}`),
  initProject: () => call<{ session_id: string }>('POST', '/api/project/init'),
  // The display settings are the only config the tab may write, and every
  // write answers the whole set.
  config: () => call<ConfigView>('GET', '/api/config'),
  setConfig: (key: string, value: unknown) => call<DisplaySettings>('POST', '/api/config/set', { key, value }),
  removeConfig: (key: string) => call<DisplaySettings>('POST', '/api/config/remove', { key }),
  // detach asks the terminal to stop web mode; the gateway answers 202 and
  // then closes every tab's stream.
  detach: () => call<void>('POST', '/api/detach'),
  renameSession: (sid: string, title: string) =>
    call<Session>('PUT', `/api/sessions/${encodeURIComponent(sid)}`, { title }),
  deleteSession: (sid: string) => call<void>('DELETE', `/api/sessions/${encodeURIComponent(sid)}`),
  exportSession: (sid: string) =>
    call<{ path: string }>('POST', `/api/sessions/${encodeURIComponent(sid)}/export`),
  automation: (sid: string, name: 'goal' | 'loop', args: string) =>
    call<{ reply: string }>('POST', `/api/sessions/${encodeURIComponent(sid)}/automation`, { name, args }),
  follow: (sid: string) => call<void>('POST', '/api/follow', { session_id: sid }),
  sessions: () => call<Session[]>('GET', '/api/sessions'),
  commands: () => call<{ commands: CommandEntry[] }>('GET', '/api/commands'),
  runCommand: (sid: string, id: string, args: Record<string, string>) =>
    call<void>('POST', '/api/commands/run', { session_id: sid, id, args }),
  attachSkill: (name: string) =>
    call<{ attachment: Attachment }>('POST', `/api/skills/${encodeURIComponent(name)}/attach`),
  setSkillDisabled: (name: string, disabled: boolean) =>
    call<void>('POST', `/api/skills/${encodeURIComponent(name)}/${disabled ? 'disable' : 'enable'}`),
  setSkillGlobal: (name: string, disabled: boolean) =>
    call<void>('POST', `/api/skills/${encodeURIComponent(name)}/global`, { disabled }),
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
  mcpServers: () => call<{ servers: McpServer[]; docker: DockerMcp }>('GET', '/api/mcp'),
  mcpAction: (
    name: string,
    action: 'start' | 'refresh' | 'enable' | 'disable' | 'enable_global' | 'disable_global' | 'auth_cancel',
  ) =>
    call<void>('POST', `/api/mcp/${encodeURIComponent(name)}/${action}`),
  mcpAuth: (name: string) => call<{ url: string }>('POST', `/api/mcp/${encodeURIComponent(name)}/auth`),
  lspServers: () => call<{ servers: LspServer[] }>('GET', '/api/lsp'),
  lspDiagnostics: (name: string) =>
    call<DiagnosticCounts>('GET', `/api/lsp/${encodeURIComponent(name)}/diagnostics`),
  lspStart: (path?: string) => call<void>('POST', '/api/lsp/start', path ? { path } : {}),
  lspStop: () => call<void>('POST', '/api/lsp/stop'),
  planHandoff: (sid: string, action: 'code' | 'code_yolo') =>
    call<void>('POST', '/api/plan/handoff', { session_id: sid, action }),
  // The answer travels in the body only: a secret in a URL would reach
  // the server log and this tab's history.
  answerSecure: (source: SecurePrompt['source'], id: string, secret: string) =>
    call<void>('POST', `/api/${source}/answer`, { id, secret }),
  cancelSecure: (source: SecurePrompt['source'], id: string) =>
    call<void>('POST', `/api/${source}/cancel`, { id }),
  signInTargets: () => call<{ providers: SignInTarget[] }>('GET', '/api/auth/providers'),
  signInKey: (provider: string, apiKey: string, model?: ModelTarget) =>
    call<SignInResult>('POST', '/api/auth/provider/key', model ? { provider, api_key: apiKey, model } : { provider, api_key: apiKey }),
  signInStart: (provider: string, model?: ModelTarget) =>
    call<SignInState>('POST', '/api/auth/provider/oauth/start', model ? { provider, model } : { provider }),
  signInStatus: () => call<SignInState>('GET', '/api/auth/provider/oauth/status'),
  signInCancel: (flowId: string) =>
    call<void>('POST', '/api/auth/provider/oauth/cancel', { flow_id: flowId }),
  signInCode: (flowId: string, code: string) =>
    call<void>('POST', '/api/auth/provider/oauth/code', { flow_id: flowId, code }),
};

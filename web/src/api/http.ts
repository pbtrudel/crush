import type { PermissionMode, QuestionAnswer, Snapshot, Session, Message } from './types';

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
  createSession: (title: string) => call<Session>('POST', '/api/sessions', { title }),
  messages: (sid: string) => call<Message[]>('GET', `/api/sessions/${encodeURIComponent(sid)}/messages`),
  sendPrompt(sid: string, prompt: string, files: File[]) {
    const form = new FormData();
    form.set('prompt', prompt);
    for (const f of files) form.append('attachments', f, f.name);
    return call<{ accepted: boolean }>('POST', `/api/sessions/${encodeURIComponent(sid)}/prompt`, form);
  },
  cancel: (sid: string) => call<void>('POST', `/api/sessions/${encodeURIComponent(sid)}/cancel`),
  clearQueue: (sid: string) => call<void>('DELETE', `/api/sessions/${encodeURIComponent(sid)}/queue`),
  grant: (id: string, action: GrantAction) => call<{ resolved: boolean }>('POST', '/api/permissions/grant', { id, action }),
  setMode: (mode: PermissionMode) => call<void>('POST', '/api/permissions/mode', { mode }),
  answer: (answers: QuestionAnswer[]) => call<{ resolved: boolean }>('POST', '/api/questions/answer', { answers }),
  cancelQuestion: () => call<{ resolved: boolean }>('POST', '/api/questions/cancel'),
  completeFiles: (q: string) => call<{ files: string[]; truncated: boolean }>('GET', `/api/complete/files?q=${encodeURIComponent(q)}`),
};

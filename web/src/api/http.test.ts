import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './http';
import { forgetToken, setToken, token } from '../state/token';

afterEach(() => {
  vi.unstubAllGlobals();
  forgetToken();
});

// stub answers every fetch with one JSON body, or an empty response for a
// status that carries none. The gateway accepts a sign-in code with a bare
// 202 and no body, so the stub does too.
function stub(status = 200, body: unknown = {}) {
  const rsp = status === 204 || status === 202 ? new Response(null, { status }) : new Response(JSON.stringify(body), { status });
  const fetch = vi.fn().mockResolvedValue(rsp);
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('the bearer token', () => {
  it('is sent on every call', async () => {
    setToken('t1');
    const fetch = stub(200, []);
    await api.sessions();
    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(init.credentials).toBe('omit');
    expect(init.headers).toMatchObject({ Authorization: 'Bearer t1' });
  });

  it('is forgotten when the gateway refuses it', async () => {
    setToken('t1');
    stub(401, { error: 'missing or invalid token', code: 'unauthorized' });
    await expect(api.sessions()).rejects.toMatchObject({ status: 401 });
    expect(token()).toBeNull();
  });

  it('is not sent when none is set', async () => {
    const fetch = stub(200, []);
    await api.sessions();
    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(init.headers).not.toHaveProperty('Authorization');
  });
});

describe('api.health', () => {
  it('sends the token and forgets one the gateway refuses', async () => {
    setToken('t1');
    const fetch = stub(200, {});
    await expect(api.health()).resolves.toBe('ok');
    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(init.headers).toMatchObject({ Authorization: 'Bearer t1' });

    stub(401, {});
    await expect(api.health()).resolves.toBe('unauthorized');
    expect(token()).toBeNull();
  });

  it('maps 401 to unauthorized, other failures to down', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })));
    await expect(api.health()).resolves.toBe('unauthorized');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 502 })));
    await expect(api.health()).resolves.toBe('down');
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')));
    await expect(api.health()).resolves.toBe('down');
  });
});

interface Row {
  name: string;
  run: () => Promise<unknown>;
  method: string;
  path: string;
  body?: unknown;
  status?: number;
  response?: unknown;
  result?: unknown;
}

const payload = {
  id: 'm1', name: 'Model One', provider: 'p1', context_window: 200000, agent_ready: true,
};

const rows: Row[] = [
  { name: 'reloadConfig', run: () => api.reloadConfig(), method: 'POST', path: '/api/config/reload', status: 204 },
  { name: 'renameSession', run: () => api.renameSession('s1', 'Parser'), method: 'PUT', path: '/api/sessions/s1', body: { title: 'Parser' } },
  { name: 'deleteSession', run: () => api.deleteSession('s1'), method: 'DELETE', path: '/api/sessions/s1', status: 204, result: undefined },
  { name: 'follow', run: () => api.follow('s1'), method: 'POST', path: '/api/follow', body: { session_id: 's1' } },
  { name: 'sessions', run: () => api.sessions(), method: 'GET', path: '/api/sessions' },
  { name: 'commands', run: () => api.commands(), method: 'GET', path: '/api/commands' },
  {
    name: 'runCommand', run: () => api.runCommand('s1', 'user:fix', { FILE: 'a.go' }),
    method: 'POST', path: '/api/commands/run', body: { session_id: 's1', id: 'user:fix', args: { FILE: 'a.go' } },
  },
  {
    name: 'attachSkill', run: () => api.attachSkill('go tests'), method: 'POST', path: '/api/skills/go%20tests/attach',
    response: { attachment: { name: 'go tests', mime_type: 'text/markdown', content: '# Go tests' } },
    result: { attachment: { name: 'go tests', mime_type: 'text/markdown', content: '# Go tests' } },
  },
  { name: 'setSkillDisabled disables', run: () => api.setSkillDisabled('go-tests', true), method: 'POST', path: '/api/skills/go-tests/disable', status: 204 },
  { name: 'setSkillDisabled enables', run: () => api.setSkillDisabled('go-tests', false), method: 'POST', path: '/api/skills/go-tests/enable', status: 204 },
  { name: 'mcpPrompts', run: () => api.mcpPrompts(), method: 'GET', path: '/api/mcp/prompts' },
  {
    name: 'runMcpPrompt', run: () => api.runMcpPrompt('s1', 'github', 'triage', { topic: 'parser' }),
    method: 'POST', path: '/api/mcp/prompts/run',
    body: { session_id: 's1', client_id: 'github', prompt_id: 'triage', args: { topic: 'parser' } },
  },
  { name: 'skills', run: () => api.skills(), method: 'GET', path: '/api/skills' },
  { name: 'reloadSkills', run: () => api.reloadSkills(), method: 'POST', path: '/api/skills/reload', status: 204 },
  { name: 'summarize', run: () => api.summarize('s1'), method: 'POST', path: '/api/sessions/s1/summarize' },
  {
    name: 'ask', run: () => api.ask('s1', 'why?'), method: 'POST', path: '/api/sessions/s1/ask',
    body: { question: 'why?' }, response: { answer: 'Recursive descent.' }, result: { answer: 'Recursive descent.' },
  },
  {
    name: 'shell', run: () => api.shell('s1', 'go test ./...'), method: 'POST', path: '/api/sessions/s1/shell',
    body: { command: 'go test ./...' }, response: { run_id: 'r1' }, result: { run_id: 'r1' },
  },
  { name: 'cancelShell', run: () => api.cancelShell('r1'), method: 'DELETE', path: '/api/shell/r1', status: 204 },
  { name: 'addNote', run: () => api.addNote('s1', 'check the parser'), method: 'POST', path: '/api/sessions/s1/notes', body: { text: 'check the parser' } },
  { name: 'providers', run: () => api.providers(), method: 'GET', path: '/api/providers' },
  {
    name: 'selectModel', run: () => api.selectModel('main', 'p1', 'm1'), method: 'POST', path: '/api/models/select',
    body: { type: 'main', provider: 'p1', model: 'm1' }, response: payload, result: payload,
  },
  {
    name: 'setReasoning effort', run: () => api.setReasoning({ effort: 'high' }), method: 'POST', path: '/api/models/reasoning',
    body: { effort: 'high' }, response: payload, result: payload,
  },
  {
    name: 'setReasoning think', run: () => api.setReasoning({ think: true }), method: 'POST', path: '/api/models/reasoning',
    body: { think: true }, response: payload, result: payload,
  },
  { name: 'mcpServers', run: () => api.mcpServers(), method: 'GET', path: '/api/mcp' },
  { name: 'mcpAction start', run: () => api.mcpAction('github', 'start'), method: 'POST', path: '/api/mcp/github/start', status: 204 },
  { name: 'mcpAction refresh', run: () => api.mcpAction('github', 'refresh'), method: 'POST', path: '/api/mcp/github/refresh', status: 204 },
  { name: 'mcpAction enable', run: () => api.mcpAction('github', 'enable'), method: 'POST', path: '/api/mcp/github/enable', status: 204 },
  { name: 'mcpAction disable', run: () => api.mcpAction('github', 'disable'), method: 'POST', path: '/api/mcp/github/disable', status: 204 },
  {
    name: 'mcpAuth', run: () => api.mcpAuth('notes'), method: 'POST', path: '/api/mcp/notes/auth',
    response: { url: 'https://auth.example/notes' }, result: { url: 'https://auth.example/notes' },
  },
  { name: 'lspServers', run: () => api.lspServers(), method: 'GET', path: '/api/lsp' },
  {
    name: 'lspDiagnostics', run: () => api.lspDiagnostics('gopls'), method: 'GET', path: '/api/lsp/gopls/diagnostics',
    response: { error: 2, warning: 1, information: 0, hint: 0 },
    result: { error: 2, warning: 1, information: 0, hint: 0 },
  },
  { name: 'lspStart', run: () => api.lspStart('internal/parser'), method: 'POST', path: '/api/lsp/start', body: { path: 'internal/parser' }, status: 204 },
  { name: 'lspStart without a path', run: () => api.lspStart(), method: 'POST', path: '/api/lsp/start', body: {}, status: 204 },
  { name: 'lspStop', run: () => api.lspStop(), method: 'POST', path: '/api/lsp/stop', status: 204 },
  {
    name: 'planHandoff', run: () => api.planHandoff('s1', 'code_yolo'), method: 'POST', path: '/api/plan/handoff',
    body: { session_id: 's1', action: 'code_yolo' },
  },
  {
    name: 'answerSecure', run: () => api.answerSecure('pinentry', 'p1', 'hunter2'), method: 'POST',
    path: '/api/pinentry/answer', body: { id: 'p1', secret: 'hunter2' }, status: 204,
  },
  {
    name: 'cancelSecure', run: () => api.cancelSecure('ssh', 's1'), method: 'POST',
    path: '/api/ssh/cancel', body: { id: 's1' }, status: 204,
  },
  {
    name: 'signInTargets', run: () => api.signInTargets(), method: 'GET', path: '/api/auth/providers',
    response: { providers: [{ id: 'copilot', name: 'GitHub Copilot', configured: false, methods: ['oauth'], oauth_kind: 'device' }] },
    result: { providers: [{ id: 'copilot', name: 'GitHub Copilot', configured: false, methods: ['oauth'], oauth_kind: 'device' }] },
  },
  {
    name: 'signInKey', run: () => api.signInKey('openai', 'sk-valid'), method: 'POST', path: '/api/auth/provider/key',
    body: { provider: 'openai', api_key: 'sk-valid' }, response: { signed_in: true, model_applied: false },
    result: { signed_in: true, model_applied: false },
  },
  {
    name: 'signInKey with a model', run: () => api.signInKey('openai', 'sk-valid', { type: 'main', model: 'gpt-5' }),
    method: 'POST', path: '/api/auth/provider/key',
    body: { provider: 'openai', api_key: 'sk-valid', model: { type: 'main', model: 'gpt-5' } },
    response: { signed_in: true, model_applied: true }, result: { signed_in: true, model_applied: true },
  },
  {
    name: 'signInStart', run: () => api.signInStart('copilot'), method: 'POST', path: '/api/auth/provider/oauth/start',
    body: { provider: 'copilot' },
    response: { flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'waiting', kind: 'device', user_code: 'WDJB-MJHT' },
    result: { flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'waiting', kind: 'device', user_code: 'WDJB-MJHT' },
  },
  {
    name: 'signInStart with a model', run: () => api.signInStart('xai', { type: 'small', model: 'grok-mini' }),
    method: 'POST', path: '/api/auth/provider/oauth/start',
    body: { provider: 'xai', model: { type: 'small', model: 'grok-mini' } },
    response: { flow_id: 'f2', provider: 'xai', provider_name: 'xAI', state: 'waiting', kind: 'browser', code_entry: true },
    result: { flow_id: 'f2', provider: 'xai', provider_name: 'xAI', state: 'waiting', kind: 'browser', code_entry: true },
  },
  {
    name: 'signInStatus', run: () => api.signInStatus(), method: 'GET', path: '/api/auth/provider/oauth/status',
    response: { flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'saving' },
    result: { flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'saving' },
  },
  {
    name: 'signInCancel', run: () => api.signInCancel('f1'), method: 'POST', path: '/api/auth/provider/oauth/cancel',
    body: { flow_id: 'f1' }, status: 204,
  },
  {
    name: 'signInCode', run: () => api.signInCode('f2', 'good-code'), method: 'POST', path: '/api/auth/provider/oauth/code',
    body: { flow_id: 'f2', code: 'good-code' }, status: 202,
  },
];

describe('api calls', () => {
  it.each(rows)('$name sends $method $path', async (row) => {
    const fetch = stub(row.status, row.response);
    const out = await row.run();
    expect(fetch).toHaveBeenCalledTimes(1);
    const [path, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(row.path);
    expect(init.method).toBe(row.method);
    expect(init.body).toBe(row.body === undefined ? undefined : JSON.stringify(row.body));
    if ('result' in row) expect(out).toEqual(row.result);
  });

  it('sends a secure answer in the body only', async () => {
    const fetch = stub(204);
    await api.answerSecure('pinentry', 'p1', 'hunter2');
    const [url, init] = fetch.mock.calls.at(-1)!;
    expect(url).toBe('/api/pinentry/answer');
    expect(String(url)).not.toContain('hunter2');
    expect(JSON.stringify(init.headers)).not.toContain('hunter2');
    expect(JSON.parse(init.body as string)).toEqual({ id: 'p1', secret: 'hunter2' });
  });

  it('sends a pasted sign-in code in the body only', async () => {
    const fetch = stub(202);
    await api.signInCode('f2', 'good-code');
    const [url, init] = fetch.mock.calls.at(-1)!;
    expect(url).toBe('/api/auth/provider/oauth/code');
    expect(JSON.stringify(init.headers)).not.toContain('good-code');
    expect(JSON.parse(init.body as string)).toEqual({ flow_id: 'f2', code: 'good-code' });
  });

  it('reports a sign-in status with no flow as a 404 no_flow', async () => {
    stub(404, { error: 'no sign-in has started', code: 'no_flow' });
    await expect(api.signInStatus()).rejects.toMatchObject({ status: 404, code: 'no_flow' });
  });

  it('turns a failed call into an ApiError with its code', async () => {
    stub(503, { error: 'Crush is starting the agent', code: 'agent_starting' });
    await expect(api.providers()).rejects.toMatchObject({ status: 503, code: 'agent_starting', message: 'Crush is starting the agent' });
  });
});

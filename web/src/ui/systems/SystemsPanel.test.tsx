import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type {
  CommandEntry, DiagnosticCounts, Frame, LspServer, McpServer, ModelChoice, PermissionRequest, SkillEntry,
} from '../../api/types';
import { ApiError, api } from '../../api/http';
import { initialState, reduce } from '../../state/reducer';
import { dispatch, state } from '../../state/store';
import { closeSystems, paletteOpen, systemsOpen } from '../../state/palette';
import { cinematic, setCinematic } from '../../state/cinematic';
import { resetWebglProbe } from '../../state/effects';
import { App } from '../App';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const originalGetContext = HTMLCanvasElement.prototype.getContext;

function counts(error: number, warning = 0): DiagnosticCounts {
  return { error, warning, information: 0, hint: 0 };
}

function mcpServer(patch: Partial<McpServer> & Pick<McpServer, 'name' | 'state'>): McpServer {
  return { tools: 0, prompts: 0, disabled: false, needs_auth: false, channel: false, ...patch };
}

const MCP: McpServer[] = [
  mcpServer({ name: 'github', state: 'connected', tools: 12 }),
  mcpServer({ name: 'notes', state: 'error', error: 'connection refused' }),
];

const LSP: LspServer[] = [
  { name: 'gopls', state: '2', diagnostics: counts(2, 1) },
  { name: 'rust-analyzer', state: '3', error: 'it crashed', diagnostics: counts(1) },
  { name: 'tsserver', state: '4', diagnostics: counts(0) },
];

function seed() {
  state.value = {
    ...reduce(initialState(), fx.snapshot),
    conn: 'live',
    lsp: {
      gopls: { state: '2', diagnostics: 3, tools: 0 },
      'rust-analyzer': { state: '3', diagnostics: 1, tools: 0 },
      tsserver: { state: '4', diagnostics: 0, tools: 0 },
    },
    mcp: {
      github: { state: 'connected', diagnostics: 0, tools: 12 },
      notes: { state: 'error', diagnostics: 0, tools: 0 },
    },
  };
}

function stubLists(mcp: McpServer[] = MCP, lsp: LspServer[] = LSP) {
  const mcpServers = vi.spyOn(api, 'mcpServers').mockResolvedValue({ servers: mcp, docker: { available: false, enabled: false } });
  const lspServers = vi.spyOn(api, 'lspServers').mockResolvedValue({ servers: lsp });
  return { mcpServers, lspServers };
}

function stubPaletteSources() {
  vi.spyOn(api, 'commands').mockResolvedValue({ commands: [] as CommandEntry[] });
  vi.spyOn(api, 'mcpPrompts').mockResolvedValue({ prompts: [] });
  vi.spyOn(api, 'skills').mockResolvedValue({ skills: [] as SkillEntry[] });
  vi.spyOn(api, 'providers').mockResolvedValue({ choices: [] as ModelChoice[] });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

async function mount() {
  await act(() => { render(<App />, root); });
  await flush();
}

function hudGroup(kind: 'lsp' | 'mcp') {
  return root.querySelector<HTMLButtonElement>(`button.hexes[data-kind="${kind}"]`)!;
}

async function openFromHud(kind: 'lsp' | 'mcp' = 'lsp') {
  const button = hudGroup(kind);
  button.focus();
  await act(() => { button.click(); });
  await flush();
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Systems"]');
}

function section(name: 'Language servers' | 'MCP servers') {
  return Array.from(root.querySelectorAll<HTMLElement>('.systems__section'))
    .find((s) => s.querySelector('h2')?.textContent === name)!;
}

function heading(name: 'Language servers' | 'MCP servers') {
  return section(name).querySelector<HTMLHeadingElement>('h2')!;
}

function card(name: string) {
  return Array.from(root.querySelectorAll<HTMLElement>('.systems__card'))
    .find((c) => c.querySelector('.systems__name')?.textContent === name);
}

function cardText(name: string, selector: string) {
  return card(name)!.querySelector(selector)!.textContent;
}

function cardButton(name: string, label: string) {
  return Array.from(card(name)!.querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => b.textContent === label)!;
}

function buttonNames(name: string): string[] {
  return Array.from(card(name)!.querySelectorAll('button')).map((b) => b.textContent ?? '');
}

function sectionButton(name: 'Language servers' | 'MCP servers', label: string) {
  return Array.from(section(name).querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => b.textContent === label)!;
}

function retryButton(name: 'Language servers' | 'MCP servers') {
  return sectionButton(name, 'Retry');
}

async function pressKey(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
  await act(() => { target.dispatchEvent(e); });
  await flush();
  return e;
}

async function click(el: Element) {
  await act(() => { (el as HTMLElement).click(); });
  await flush();
}

function lspFrame(name: string, stateValue: number, diagnostics: number): Frame {
  return {
    seq: 90,
    type: 'lsp_event',
    payload: {
      type: 'updated',
      payload: { type: 'state_changed', name, state: stateValue, diagnostic_count: diagnostics },
    },
  } as unknown as Frame;
}

function mcpFrame(name: string, stateValue: string, tools = 0): Frame {
  return {
    seq: 91,
    type: 'mcp_event',
    payload: { type: 'updated', payload: { type: 'state_changed', name, state: stateValue, tool_count: tools } },
  } as unknown as Frame;
}

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof originalGetContext;
  resetWebglProbe();
  document.body.append(root);
  vi.spyOn(api, 'mcpAction').mockResolvedValue(undefined);
  vi.spyOn(api, 'mcpAuth').mockResolvedValue({ url: '' });
  vi.spyOn(api, 'lspStart').mockResolvedValue(undefined);
  vi.spyOn(api, 'lspStop').mockResolvedValue(undefined);
  vi.spyOn(api, 'lspDiagnostics').mockResolvedValue(counts(0));
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  closeSystems();
  if (paletteOpen.value !== null) paletteOpen.value = null;
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = originalGetContext;
  resetWebglProbe();
  vi.restoreAllMocks();
});

describe('SystemsPanel servers', () => {
  it('lists MCP servers with their tools and state', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    expect(cardText('github', '.systems__state')).toBe('running');
    expect(cardText('github', '.systems__counts')).toBe('12 tools');
    expect(cardText('notes', '.systems__state')).toBe('error: connection refused');

    const tile = card('notes')!.querySelector('.hex')!;
    expect(tile.getAttribute('data-tone')).toBe('error');
    expect(tile.getAttribute('aria-hidden')).toBe('true');
    expect(tile.querySelector('.hex__bang')!.textContent).toBe('!');
  });

  it('merges live data over the fetched list', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    await act(() => { dispatch((s) => reduce(s, mcpFrame('github', 'connected', 2))); });
    await flush();

    expect(cardText('github', '.systems__counts')).toBe('2 tools');
    expect(cardText('github', '.systems__state')).toBe('running');
  });

  it('lists LSP servers with their diagnostics and error text', async () => {
    seed();
    stubLists([...MCP], [
      ...LSP,
      // Only the list knows this one; it renders from the list.
      { name: 'clangd', state: '0', diagnostics: counts(0) },
    ]);
    await mount();
    await openFromHud('lsp');

    expect(cardText('gopls', '.systems__state')).toBe('ready');
    expect(cardText('gopls', '.systems__counts')).toBe('2 errors, 1 warning');
    expect(cardText('rust-analyzer', '.systems__state')).toBe('error: it crashed');
    expect(cardText('rust-analyzer', '.systems__counts')).toBe('1 error');
    expect(cardText('tsserver', '.systems__counts')).toBe('no problems');
    expect(cardText('clangd', '.systems__state')).toBe('not started');
    expect(cardText('clangd', '.systems__counts')).toBe('no problems');
  });

  it('renders a server only the live frames know', async () => {
    seed();
    stubLists();
    await act(() => { dispatch((s) => reduce(s, lspFrame('zls', 2, 1))); });
    await flush();
    await mount();
    await openFromHud('lsp');

    expect(cardText('zls', '.systems__state')).toBe('ready');
    expect(cardText('zls', '.systems__counts')).toBe('1 diagnostic');
    expect(api.lspDiagnostics).not.toHaveBeenCalled();
  });

  it('updates a row from a live frame without refetching the list', async () => {
    seed();
    const { lspServers } = stubLists();
    await mount();
    await openFromHud('lsp');

    await act(() => { dispatch((s) => reduce(s, lspFrame('gopls', 4, 3))); });
    await flush();

    expect(cardText('gopls', '.systems__state')).toBe('stopped');
    expect(lspServers).toHaveBeenCalledTimes(1);
    expect(api.lspDiagnostics).not.toHaveBeenCalled();
  });

  it('refetches only the changed diagnostic counts', async () => {
    seed();
    const { lspServers } = stubLists();
    vi.mocked(api.lspDiagnostics).mockResolvedValue(counts(4, 1));
    await mount();
    await openFromHud('lsp');

    await act(() => { dispatch((s) => reduce(s, lspFrame('gopls', 2, 5))); });
    await flush();

    expect(api.lspDiagnostics).toHaveBeenCalledWith('gopls');
    expect(api.lspDiagnostics).toHaveBeenCalledTimes(1);
    expect(cardText('gopls', '.systems__counts')).toBe('4 errors, 1 warning');
    expect(lspServers).toHaveBeenCalledTimes(1);
  });

  it('names a diagnostic refresh that failed', async () => {
    seed();
    stubLists();
    vi.mocked(api.lspDiagnostics).mockRejectedValue(new ApiError(500, 'internal', 'counts failed'));
    await mount();
    await openFromHud('lsp');

    await act(() => { dispatch((s) => reduce(s, lspFrame('gopls', 2, 5))); });
    await flush();

    const status = Array.from(dialog()!.querySelectorAll('.systems__status'))
      .find((el) => el.getAttribute('data-tone') === 'error')!;
    expect(status.textContent).toContain('counts failed');
    expect(status.getAttribute('role')).toBe('alert');
  });

  it('keeps one diagnostic request in flight per server', async () => {
    seed();
    stubLists();
    const first = deferred<DiagnosticCounts>();
    const second = deferred<DiagnosticCounts>();
    vi.mocked(api.lspDiagnostics)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    await mount();
    await openFromHud('lsp');

    await act(() => { dispatch((s) => reduce(s, lspFrame('gopls', 2, 5))); });
    await flush();
    expect(api.lspDiagnostics).toHaveBeenCalledTimes(1);

    await act(() => { dispatch((s) => reduce(s, lspFrame('gopls', 2, 6))); });
    await flush();
    expect(api.lspDiagnostics).toHaveBeenCalledTimes(1);

    await act(async () => { first.resolve(counts(4, 1)); await first.promise; });
    await flush();
    expect(api.lspDiagnostics).toHaveBeenCalledTimes(2);

    await act(async () => { second.resolve(counts(5, 1)); await second.promise; });
    await flush();
    expect(cardText('gopls', '.systems__counts')).toBe('5 errors, 1 warning');
  });
});

describe('SystemsPanel actions', () => {
  it('runs the MCP action each state offers', async () => {
    seed();
    state.value = { ...state.value, mcp: { ...state.value.mcp, legacy: { state: 'disabled', diagnostics: 0, tools: 0 } } };
    stubLists([...MCP, mcpServer({ name: 'legacy', state: 'disabled', disabled: true })]);
    await mount();
    await openFromHud('mcp');

    expect(buttonNames('github')).toEqual(['Refresh', 'Disable']);
    expect(buttonNames('notes')).toEqual(['Start', 'Disable']);
    expect(buttonNames('legacy')).toEqual(['Enable']);

    await click(cardButton('github', 'Refresh'));
    expect(api.mcpAction).toHaveBeenCalledWith('github', 'refresh');
    await click(cardButton('github', 'Disable'));
    expect(api.mcpAction).toHaveBeenCalledWith('github', 'disable');
    await click(cardButton('notes', 'Start'));
    expect(api.mcpAction).toHaveBeenCalledWith('notes', 'start');
    await click(cardButton('legacy', 'Enable'));
    expect(api.mcpAction).toHaveBeenCalledWith('legacy', 'enable');
  });

  it('starts and stops all language servers from the section', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('lsp');

    await click(sectionButton('Language servers', 'Start language servers'));
    expect(api.lspStart).toHaveBeenCalledTimes(1);
    expect(api.lspStart).toHaveBeenCalledWith();
    await click(sectionButton('Language servers', 'Stop language servers'));
    expect(api.lspStop).toHaveBeenCalledTimes(1);
  });

  it('keeps an in-flight button enabled and ignores a second press', async () => {
    seed();
    stubLists();
    const pending = deferred<void>();
    vi.mocked(api.mcpAction).mockReturnValueOnce(pending.promise);
    await mount();
    await openFromHud('mcp');

    const refresh = cardButton('github', 'Refresh');
    refresh.focus();
    await click(refresh);
    expect(refresh.getAttribute('aria-disabled')).toBe('true');
    expect(refresh.disabled).toBe(false);
    expect(document.activeElement).toBe(refresh);

    await click(refresh);
    expect(api.mcpAction).toHaveBeenCalledTimes(1);

    await act(async () => { pending.resolve(); await pending.promise; });
    await flush();
    expect(refresh.getAttribute('aria-disabled')).toBeNull();
    expect(document.activeElement).toBe(refresh);
  });

  it('turns Disable into Enable when a frame says the server is disabled', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    const disable = cardButton('github', 'Disable');
    disable.focus();
    await click(disable);
    await act(() => { dispatch((s) => reduce(s, mcpFrame('github', 'disabled', 12))); });
    await flush();

    expect(cardText('github', '.systems__state')).toBe('disabled');
    expect(buttonNames('github')).toEqual(['Enable']);
    expect(card('github')!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it('turns Enable into Disable and Refresh as the server comes up', async () => {
    seed();
    state.value = { ...state.value, mcp: { ...state.value.mcp, legacy: { state: 'disabled', diagnostics: 0, tools: 0 } } };
    stubLists([...MCP, mcpServer({ name: 'legacy', state: 'disabled', disabled: true })]);
    await mount();
    await openFromHud('mcp');

    const enable = cardButton('legacy', 'Enable');
    enable.focus();
    await click(enable);
    await act(() => { dispatch((s) => reduce(s, mcpFrame('legacy', 'starting', 0))); });
    await flush();
    expect(buttonNames('legacy')).toEqual(['Disable']);

    await act(() => { dispatch((s) => reduce(s, mcpFrame('legacy', 'connected', 3))); });
    await flush();
    expect(buttonNames('legacy')).toEqual(['Refresh', 'Disable']);
    expect(card('legacy')!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it('takes the disabled flag from the list when no frame knows the server', async () => {
    seed();
    stubLists([...MCP, mcpServer({ name: 'idle', state: 'disabled', disabled: true })]);
    await mount();
    await openFromHud('mcp');

    expect(buttonNames('idle')).toEqual(['Enable']);
  });

  it('keeps focus inside the card when the pressed button goes away', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    const start = cardButton('notes', 'Start');
    start.focus();
    await click(start);
    await act(() => { dispatch((s) => reduce(s, mcpFrame('notes', 'connected', 0))); });
    await flush();

    expect(card('notes')).not.toBeUndefined();
    expect(card('notes')!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it('shows an action failure inline', async () => {
    seed();
    stubLists();
    vi.mocked(api.mcpAction).mockRejectedValueOnce(new ApiError(500, 'internal', 'the refresh failed'));
    await mount();
    await openFromHud('mcp');

    await click(cardButton('github', 'Refresh'));

    const status = card('github')!.querySelector('[role="alert"]')!;
    expect(status.textContent).toContain('the refresh failed');
    expect(status.getAttribute('data-tone')).toBe('error');
  });
});

describe('SystemsPanel authentication', () => {
  function authServer(): McpServer[] {
    return [mcpServer({ name: 'notes', state: 'needs auth', needs_auth: true })];
  }

  async function openAuth(url: string) {
    seed();
    state.value = { ...state.value, mcp: { notes: { state: 'needs auth', diagnostics: 0, tools: 0 } } };
    stubLists(authServer());
    vi.mocked(api.mcpAuth).mockResolvedValue({ url });
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    await mount();
    await openFromHud('mcp');
    return open;
  }

  it('opens the sign-in page in a new tab and links the same URL', async () => {
    const open = await openAuth('https://auth.example/notes');

    expect(buttonNames('notes')).toEqual(['Authenticate', 'Disable']);
    await click(cardButton('notes', 'Authenticate'));

    expect(api.mcpAuth).toHaveBeenCalledWith('notes');
    expect(open).toHaveBeenCalledWith('https://auth.example/notes', '_blank', 'noopener,noreferrer');
    const link = card('notes')!.querySelector('a')!;
    expect(link.textContent).toBe('Open the sign-in page');
    expect(link.getAttribute('href')).toBe('https://auth.example/notes');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('asks the user to finish in the terminal when there is no URL', async () => {
    const open = await openAuth('');

    await click(cardButton('notes', 'Authenticate'));

    expect(open).not.toHaveBeenCalled();
    expect(card('notes')!.querySelector('a')).toBeNull();
    expect(cardText('notes', '.systems__note')).toBe('Finish signing in from the terminal');
  });

  it('never opens or links a URL that is not http or https', async () => {
    const open = await openAuth('javascript:alert(1)');

    await click(cardButton('notes', 'Authenticate'));

    expect(open).not.toHaveBeenCalled();
    expect(card('notes')!.querySelector('a')).toBeNull();
    const alert = card('notes')!.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe('The MCP server did not return a web address');
  });

  it('drops Authenticate once a frame says the server is connected', async () => {
    seed();
    state.value = { ...state.value, mcp: { notes: { state: 'needs auth', diagnostics: 0, tools: 0 } } };
    stubLists([mcpServer({ name: 'notes', state: 'needs auth', needs_auth: true })]);
    await mount();
    await openFromHud('mcp');

    const auth = cardButton('notes', 'Authenticate');
    auth.focus();
    await click(auth);
    await act(() => { dispatch((s) => reduce(s, mcpFrame('notes', 'connected', 4))); });
    await flush();

    expect(buttonNames('notes')).toEqual(['Refresh', 'Disable']);
    expect(card('notes')!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it('shows the server message when auth is not needed or the URL is refused', async () => {
    await openAuth('');
    vi.mocked(api.mcpAuth)
      .mockRejectedValueOnce(new ApiError(409, 'auth_not_needed', 'the MCP server is not waiting for authentication'))
      .mockRejectedValueOnce(new ApiError(502, 'bad_auth_url', 'the MCP server did not return a web address'));

    await click(cardButton('notes', 'Authenticate'));
    expect(card('notes')!.querySelector('[role="alert"]')!.textContent)
      .toContain('the MCP server is not waiting for authentication');

    await click(cardButton('notes', 'Authenticate'));
    expect(card('notes')!.querySelector('[role="alert"]')!.textContent)
      .toContain('the MCP server did not return a web address');
  });
});

describe('SystemsPanel focus and Escape', () => {
  it('traps focus and restores it to the HUD button', async () => {
    seed();
    stubLists();
    await mount();
    const button = hudGroup('lsp');
    button.focus();
    await act(() => { button.click(); });
    await flush();

    expect(document.activeElement).toBe(heading('Language servers'));

    const focusables = Array.from(dialog()!.querySelectorAll<HTMLElement>('button, a[href]'));
    const last = focusables[focusables.length - 1];
    last.focus();
    await pressKey(last, { key: 'Tab' });
    expect(document.activeElement).toBe(focusables[0]);

    await pressKey(window, { key: 'Escape', code: 'Escape' });
    expect(systemsOpen.value).toBeNull();
    expect(document.activeElement).toBe(button);
  });

  it('opens at the section its HUD group names', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    expect(document.activeElement).toBe(heading('MCP servers'));
  });

  it('closes before the inspector and the cinematic view', async () => {
    seed();
    stubLists();
    setCinematic(true);
    state.value = {
      ...state.value,
      inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false },
    };
    await mount();
    await openFromHud('lsp');

    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(systemsOpen.value).toBeNull();
    expect(state.value.inspected?.sessionId).toBe('sub-1');
    expect(cinematic.value).toBe(true);
  });

  it('gives way to the palette on Ctrl+K', async () => {
    seed();
    stubLists();
    stubPaletteSources();
    await mount();
    await openFromHud('lsp');

    await pressKey(window, { key: 'k', code: 'KeyK', ctrlKey: true });

    expect(systemsOpen.value).toBeNull();
    expect(paletteOpen.value).toBe('all');
  });

  it('is inert while a permission request is open', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('lsp');

    const layer = root.querySelector('.systems-layer')!;
    expect(layer.hasAttribute('inert')).toBe(false);
    await act(() => {
      state.value = {
        ...state.value,
        permissions: [{
          id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
          description: 'Run the tests', action: 'execute', params: {}, path: '/tmp/project',
        }],
      };
    });
    expect(layer.hasAttribute('inert')).toBe(true);
  });

  it('keeps the focus in the panel across a prompt', async () => {
    seed();
    stubLists();
    await mount();
    await openFromHud('mcp');

    // A card action the user tabbed to, without pressing it: the panel is
    // the remembered control the prompt interrupts.
    const disable = cardButton('github', 'Disable');
    disable.focus();
    expect(document.activeElement).toBe(disable);

    // A prompt makes the page behind it inert, and the browser drops the
    // focus to the body before the prompt takes it.
    (document.activeElement as HTMLElement | null)?.blur();
    expect(document.activeElement).toBe(document.body);

    const prompt: PermissionRequest = {
      id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
      description: 'Run the tests', action: 'execute', params: {}, path: '/tmp/project',
    };
    await act(() => { state.value = { ...state.value, permissions: [prompt] }; });
    await flush();
    const panel = dialog()!;
    expect(root.querySelector('.systems-layer')!.hasAttribute('inert')).toBe(true);
    expect(panel.contains(document.activeElement)).toBe(false);

    // A prompt that still needs the user owns the focus, so a later render
    // of the panel under it does not take it back.
    await act(() => { state.value = { ...state.value, permissions: [prompt, { ...prompt, id: 'p2' }] }; });
    await flush();
    expect(panel.contains(document.activeElement)).toBe(false);

    // The prompt closes: the panel takes the control that had the focus back.
    await act(() => { state.value = { ...state.value, permissions: [] }; });
    await flush();
    expect(document.activeElement).toBe(disable);
  });
});

describe('SystemsPanel loading', () => {
  it('loads each section on its own and retries the failing one', async () => {
    seed();
    vi.spyOn(api, 'mcpServers')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce({ servers: MCP, docker: { available: false, enabled: false } });
    vi.spyOn(api, 'lspServers').mockResolvedValue({ servers: LSP });
    await mount();
    await openFromHud('mcp');

    const status = section('MCP servers').querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    expect(section('MCP servers').querySelector('[role="alert"]')).toBeNull();
    expect(card('gopls')).not.toBeUndefined();

    await click(retryButton('MCP servers'));

    expect(api.mcpServers).toHaveBeenCalledTimes(2);
    expect(card('github')).not.toBeUndefined();
    expect(section('MCP servers').querySelector('[role="status"]')).toBeNull();
    expect(document.activeElement).toBe(heading('MCP servers'));
  });

  it('shows a failing list as an error with a retry', async () => {
    seed();
    stubLists();
    vi.mocked(api.mcpServers).mockRejectedValue(new ApiError(500, 'internal', 'the servers could not be listed'));
    await mount();
    await openFromHud('mcp');

    const alert = section('MCP servers').querySelector('[role="alert"]')!;
    expect(alert.textContent).toContain('the servers could not be listed');
    expect(retryButton('MCP servers')).not.toBeUndefined();
  });

  it('drops a late list answer after the panel closes', async () => {
    seed();
    const late = deferred<{ servers: McpServer[]; docker: { available: boolean; enabled: boolean } }>();
    vi.spyOn(api, 'mcpServers').mockReturnValueOnce(late.promise);
    vi.spyOn(api, 'lspServers').mockResolvedValue({ servers: LSP });
    await mount();
    await openFromHud('mcp');

    await pressKey(window, { key: 'Escape', code: 'Escape' });
    expect(dialog()).toBeNull();

    await act(async () => { late.resolve({ servers: MCP, docker: { available: false, enabled: false } }); await Promise.resolve(); });
    await flush();

    expect(dialog()).toBeNull();
    expect(systemsOpen.value).toBeNull();
  });

  it('drops an older list answer that a newer load replaced', async () => {
    seed();
    // The rows come from the list alone, so an older answer would show.
    state.value = { ...state.value, mcp: {} };
    const fresh = mcpServer({ name: 'fresh', state: 'connected', tools: 1 });
    const older = deferred<{ servers: McpServer[]; docker: { available: boolean; enabled: boolean } }>();
    const newer = deferred<{ servers: McpServer[]; docker: { available: boolean; enabled: boolean } }>();
    vi.spyOn(api, 'mcpServers')
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'the list failed'))
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    vi.spyOn(api, 'lspServers').mockResolvedValue({ servers: LSP });
    await mount();
    await openFromHud('mcp');

    await click(retryButton('MCP servers'));
    await click(retryButton('MCP servers'));

    await act(async () => { newer.resolve({ servers: [fresh], docker: { available: false, enabled: false } }); await Promise.resolve(); });
    await flush();
    expect(card('fresh')).not.toBeUndefined();

    await act(async () => { older.resolve({ servers: MCP, docker: { available: false, enabled: false } }); await Promise.resolve(); });
    await flush();
    expect(card('fresh')).not.toBeUndefined();
    expect(card('github')).toBeUndefined();
    expect(card('notes')).toBeUndefined();
  });
});

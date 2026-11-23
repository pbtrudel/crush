import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { CommandEntry, Frame, McpPrompt, ModelChoice, Session, SkillEntry } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { initialState, reduce } from '../../state/reducer';
import { dispatch, state } from '../../state/store';
import { closePalette, closeSessions, paletteOpen, sessionsOpen } from '../../state/palette';
import { resetWebglProbe } from '../../state/effects';
import { setCinematic } from '../../state/cinematic';
import { App } from '../App';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const originalGetContext = HTMLCanvasElement.prototype.getContext;

function session(id: string, title: string): Session {
  return {
    id, parent_session_id: '', title, message_count: 0, prompt_tokens: 0, completion_tokens: 0,
    cost: 0, is_busy: false, created_at: 0, updated_at: 0,
  };
}

// The server's order is the list's order: newest first, is s-1 s-2 s-3 here.
const LIST = [session('s-1', 'Fix the parser'), session('s-2', 'Fix the lexer'), session('s-3', 'Port the parser')];

function seed() {
  state.value = {
    ...reduce(initialState(), fx.snapshot),
    conn: 'live',
    sessionId: 's-1',
    session: session('s-1', 'Fix the parser'),
  };
}

interface Stub {
  commands?: CommandEntry[];
  prompts?: McpPrompt[];
  skills?: SkillEntry[];
  models?: ModelChoice[];
}

function stubPaletteSources(patch: Stub = {}) {
  vi.spyOn(api, 'commands').mockResolvedValue({ commands: patch.commands ?? [] });
  vi.spyOn(api, 'mcpPrompts').mockResolvedValue({ prompts: patch.prompts ?? [] });
  vi.spyOn(api, 'skills').mockResolvedValue({ skills: patch.skills ?? [] });
  vi.spyOn(api, 'providers').mockResolvedValue({ choices: patch.models ?? [] });
}

function stubSessions(list: Session[] = LIST) {
  return vi.spyOn(api, 'sessions').mockResolvedValue(list);
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

function hudButton() {
  return root.querySelector<HTMLButtonElement>('h1.hud__title button')!;
}

function hudTitle() {
  return root.querySelector('h1.hud__title')!.textContent;
}

async function openFromHud() {
  const button = hudButton();
  button.focus();
  await act(() => { button.click(); });
  await flush();
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Sessions"]');
}

function rows(): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('.sessions__row'));
}

function row(title: string): HTMLElement {
  return rows().find((r) => r.querySelector('.sessions__title')?.textContent === title)!;
}

function titleButton(title: string): HTMLButtonElement {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('.sessions__title'))
    .find((b) => b.textContent === title)!;
}

function rowButton(title: string, name: string): HTMLButtonElement {
  return Array.from(row(title).querySelectorAll('button')).find((b) => b.textContent === name)!;
}

function confirmDialog(title: string) {
  return row(title).querySelector<HTMLElement>('[role="alertdialog"]');
}

function confirmButton(title: string, name: string) {
  return Array.from(confirmDialog(title)!.querySelectorAll('button')).find((b) => b.textContent === name)!;
}

function filter() {
  return root.querySelector<HTMLInputElement>('input[aria-label="Filter sessions"]')!;
}

async function type(value: string) {
  const input = filter();
  await act(() => {
    input.value = value;
    input.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
  await flush();
}

async function pressKey(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
  await act(() => { target.dispatchEvent(e); });
  await flush();
  return e;
}

beforeEach(() => {
  // Cinematic mode needs a live 3D scene, so the same probe stub as the HUD's
  // cinematic test lets setCinematic stick.
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof originalGetContext;
  resetWebglProbe();
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  closeSessions();
  closePalette();
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = originalGetContext;
  resetWebglProbe();
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('session switcher', () => {
  it('opens from the HUD title', async () => {
    seed();
    stubSessions();
    await mount();

    expect(hudButton().getAttribute('aria-label')).toBe('Session: Fix the parser. Switch session');
    await openFromHud();

    expect(dialog()).not.toBeNull();
    expect(Array.from(dialog()!.querySelectorAll('.sessions__title')).map((b) => b.textContent))
      .toEqual(['Fix the parser', 'Fix the lexer', 'Port the parser']);
  });

  it('filters as you type', async () => {
    seed();
    stubSessions();
    await mount();
    await openFromHud();

    await type('lexer');
    expect(Array.from(dialog()!.querySelectorAll('.sessions__title')).map((b) => b.textContent))
      .toEqual(['Fix the lexer']);

    await type('nothing');
    expect(dialog()!.textContent).toContain('No sessions match');
  });

  it('opens a session in the terminal', async () => {
    seed();
    stubSessions();
    const follow = vi.spyOn(api, 'follow').mockResolvedValue(undefined);
    await mount();
    await openFromHud();

    await act(() => { titleButton('Fix the lexer').click(); });
    await flush();

    expect(follow).toHaveBeenCalledWith('s-2');
    expect(sessionsOpen.value).toBe(false);
    expect(dialog()).toBeNull();
    // The tab has not moved yet: only the frame from the terminal moves it.
    expect(state.value.sessionId).toBe('s-1');

    await act(() => {
      dispatch((s) => reduce(s, { seq: 20, type: 'session_follow', payload: { session_id: 's-2' } }));
    });
    expect(state.value.sessionId).toBe('s-2');
  });

  it('closes on the current session without asking the terminal', async () => {
    seed();
    stubSessions();
    const follow = vi.spyOn(api, 'follow').mockResolvedValue(undefined);
    await mount();
    await openFromHud();

    await act(() => { titleButton('Fix the parser').click(); });
    await flush();

    expect(follow).not.toHaveBeenCalled();
    expect(sessionsOpen.value).toBe(false);
  });

  it('keeps the dialog and the failure line when following fails', async () => {
    seed();
    stubSessions();
    vi.spyOn(api, 'follow').mockRejectedValue(new ApiError(404, 'unknown_session', 'no such session'));
    await mount();
    await openFromHud();

    await act(() => { titleButton('Fix the lexer').click(); });
    await flush();

    expect(sessionsOpen.value).toBe(true);
    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toContain('no such session');
  });

  it('renames in place and reloads the list', async () => {
    seed();
    const sessions = vi.spyOn(api, 'sessions')
      .mockResolvedValueOnce(LIST)
      .mockResolvedValueOnce([session('s-1', 'Fix the parser'), session('s-2', 'New name'), session('s-3', 'Port the parser')]);
    const rename = vi.spyOn(api, 'renameSession').mockResolvedValue(session('s-2', 'New name'));
    await mount();
    await openFromHud();

    await act(() => { rowButton('Fix the lexer', 'Rename').click(); });
    await flush();
    const input = root.querySelector<HTMLInputElement>('input[aria-label="Rename Fix the lexer"]')!;
    expect(input.value).toBe('Fix the lexer');

    await act(() => {
      input.value = 'New name';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await pressKey(input, { key: 'Enter', code: 'Enter' });

    expect(rename).toHaveBeenCalledWith('s-2', 'New name');
    expect(sessions).toHaveBeenCalledTimes(2);
    expect(root.querySelector('.sessions__rename')).toBeNull();
    expect(Array.from(dialog()!.querySelectorAll('.sessions__title')).map((b) => b.textContent))
      .toEqual(['Fix the parser', 'New name', 'Port the parser']);
    // The HUD title moves only on the session frame, never on this answer.
    expect(hudTitle()).toBe('Fix the parser');
  });

  it('cancels a rename on Escape and leaves the dialog open', async () => {
    seed();
    stubSessions();
    const rename = vi.spyOn(api, 'renameSession').mockResolvedValue(session('s-2', 'New name'));
    await mount();
    await openFromHud();

    await act(() => { rowButton('Fix the lexer', 'Rename').click(); });
    await flush();
    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(root.querySelector('.sessions__rename')).toBeNull();
    expect(titleButton('Fix the lexer')).not.toBeNull();
    expect(rename).not.toHaveBeenCalled();
    expect(sessionsOpen.value).toBe(true);
  });

  it('shows a refused rename inline', async () => {
    seed();
    stubSessions();
    vi.spyOn(api, 'renameSession').mockRejectedValue(new ApiError(400, 'bad_title', 'title must be 1 to 200 characters'));
    await mount();
    await openFromHud();

    await act(() => { rowButton('Fix the lexer', 'Rename').click(); });
    await flush();
    await pressKey(root.querySelector('.sessions__rename')!, { key: 'Enter', code: 'Enter' });

    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toContain('title must be 1 to 200 characters');
    expect(sessionsOpen.value).toBe(true);
  });

  it('asks before deleting and refuses the current session', async () => {
    seed();
    stubSessions();
    const remove = vi.spyOn(api, 'deleteSession').mockResolvedValue(undefined);
    await mount();
    await openFromHud();

    const refuse = rowButton('Fix the parser', 'Delete');
    expect(refuse.disabled).toBe(true);
    const reason = root.querySelector<HTMLElement>(`#${refuse.getAttribute('aria-describedby')}`)!;
    expect(reason.textContent).toBe('The terminal is showing this session');

    await act(() => { rowButton('Fix the lexer', 'Delete').click(); });
    await flush();

    expect(confirmDialog('Fix the lexer')!.textContent).toContain('Delete “Fix the lexer”?');
    expect(confirmButton('Fix the lexer', 'Cancel')).not.toBeNull();
    await act(() => { confirmButton('Fix the lexer', 'Cancel').click(); });
    await flush();
    expect(confirmDialog('Fix the lexer')).toBeNull();
    expect(remove).not.toHaveBeenCalled();
  });

  it('deletes another session after the confirm and reloads the list', async () => {
    seed();
    const sessions = vi.spyOn(api, 'sessions')
      .mockResolvedValueOnce(LIST)
      .mockResolvedValueOnce([session('s-1', 'Fix the parser'), session('s-2', 'Fix the lexer')]);
    const remove = vi.spyOn(api, 'deleteSession').mockResolvedValue(undefined);
    await mount();
    await openFromHud();

    await act(() => { rowButton('Port the parser', 'Delete').click(); });
    await flush();
    await act(() => { confirmButton('Port the parser', 'Delete').click(); });
    await flush();

    expect(remove).toHaveBeenCalledWith('s-3');
    expect(sessions).toHaveBeenCalledTimes(2);
    expect(Array.from(dialog()!.querySelectorAll('.sessions__title')).map((b) => b.textContent))
      .toEqual(['Fix the parser', 'Fix the lexer']);
    expect(sessionsOpen.value).toBe(true);
  });

  it('keeps a busy session and shows the server message', async () => {
    seed();
    const sessions = stubSessions();
    vi.spyOn(api, 'deleteSession').mockRejectedValue(new ApiError(409, 'session_busy', 'Crush is working in this session'));
    await mount();
    await openFromHud();

    await act(() => { rowButton('Fix the lexer', 'Delete').click(); });
    await flush();
    await act(() => { confirmButton('Fix the lexer', 'Delete').click(); });
    await flush();

    expect(titleButton('Fix the lexer')).not.toBeNull();
    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toContain('Crush is working in this session');
    expect(sessions).toHaveBeenCalledTimes(1);
  });

  it('cancels a confirm on Escape before closing', async () => {
    seed();
    stubSessions();
    await mount();
    await openFromHud();
    await act(() => { rowButton('Fix the lexer', 'Delete').click(); });
    await flush();

    await pressKey(window, { key: 'Escape', code: 'Escape' });
    expect(confirmDialog('Fix the lexer')).toBeNull();
    expect(sessionsOpen.value).toBe(true);

    await pressKey(window, { key: 'Escape', code: 'Escape' });
    expect(sessionsOpen.value).toBe(false);
  });

  it('restores focus to the HUD button on close', async () => {
    seed();
    stubSessions();
    state.value = {
      ...state.value,
      inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false },
    };
    await mount();
    const button = hudButton();
    await openFromHud();
    expect(dialog()!.contains(document.activeElement)).toBe(true);

    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(sessionsOpen.value).toBe(false);
    expect(document.activeElement).toBe(button);
    // The switcher takes Escape before the inspector.
    expect(state.value.inspected?.sessionId).toBe('sub-1');
  });

  it('shows a load failure with a Retry', async () => {
    seed();
    const sessions = vi.spyOn(api, 'sessions')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce(LIST);
    await mount();
    await openFromHud();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    await act(() => { retry.click(); });
    await flush();

    expect(sessions).toHaveBeenCalledTimes(2);
    expect(dialog()!.querySelector('[role="status"]')).toBeNull();
    expect(titleButton('Fix the lexer')).not.toBeNull();
  });

  it('gives way to the palette on Ctrl+K', async () => {
    seed();
    stubSessions();
    stubPaletteSources();
    await mount();
    await openFromHud();

    await pressKey(window, { key: 'k', code: 'KeyK', ctrlKey: true });

    expect(sessionsOpen.value).toBe(false);
    expect(paletteOpen.value).toBe('all');
    expect(root.querySelector('[aria-label="Command palette"]')).not.toBeNull();
  });

  it('is inert while a permission request is open', async () => {
    seed();
    stubSessions();
    await mount();
    await openFromHud();

    const layer = root.querySelector('.sessions-layer')!;
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

  it('keeps the title button out of the tab order in cinematic mode', async () => {
    seed();
    stubSessions();
    setCinematic(true);
    await mount();

    expect(hudButton().tabIndex).toBe(-1);
    setCinematic(false);
    await act(() => { state.value = { ...state.value }; });
    expect(hudButton().tabIndex).toBe(0);
  });
});

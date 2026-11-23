import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type {
  CommandEntry, Frame, McpPrompt, ModelChoice, Session, SkillEntry,
} from '../../api/types';
import { ApiError, api } from '../../api/http';
import { initialState, reduce } from '../../state/reducer';
import { state } from '../../state/store';
import {
  argumentItem, closePalette, closeSessions, openPalette, paletteOpen, paletteQuery, sessionsOpen,
} from '../../state/palette';
import { setCinematic } from '../../state/cinematic';
import { setMotion } from '../../state/motion';
import { App } from '../App';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');

function session(id: string, title: string): Session {
  return {
    id, parent_session_id: '', title, message_count: 0, prompt_tokens: 0, completion_tokens: 0,
    cost: 0, is_busy: false, created_at: 0, updated_at: 0,
  };
}

interface Stub {
  commands?: CommandEntry[];
  prompts?: McpPrompt[];
  skills?: SkillEntry[];
  sessions?: Session[];
  models?: ModelChoice[];
}

function stubSources(patch: Stub = {}) {
  vi.spyOn(api, 'commands').mockResolvedValue({
    commands: patch.commands ?? [
      { id: 'user:fix', title: 'user:fix', kind: 'custom', args: [{ id: 'FILE', title: 'FILE', required: true }] },
    ],
  });
  vi.spyOn(api, 'mcpPrompts').mockResolvedValue({ prompts: patch.prompts ?? [] });
  vi.spyOn(api, 'skills').mockResolvedValue({
    skills: patch.skills ?? [{ name: 'go-tests', description: 'Write Go tests', disabled: false }],
  });
  vi.spyOn(api, 'sessions').mockResolvedValue(patch.sessions ?? [session('s-2', 'Fix the lexer')]);
  vi.spyOn(api, 'providers').mockResolvedValue({ choices: patch.models ?? [] });
}

function seed(patch: Partial<typeof state.value> = {}) {
  state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live', ...patch };
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

async function mount() {
  await act(() => { render(<App />, root); });
  await flush();
}

function deck() {
  return root.querySelector<HTMLTextAreaElement>('#deck-input')!;
}

function combobox() {
  return root.querySelector<HTMLInputElement>('[role="combobox"]');
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"]');
}

function retryButton() {
  return [...root.querySelectorAll('button')].find((b) => b.textContent === 'Retry') ?? null;
}

async function pressKey(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
  await act(() => { target.dispatchEvent(e); });
  await flush();
  return e;
}

async function openWithKey(metaKey = false) {
  deck().focus();
  return pressKey(deck(), { key: 'k', code: 'KeyK', ctrlKey: !metaKey, metaKey });
}

async function type(value: string) {
  const box = combobox()!;
  await act(() => {
    box.value = value;
    box.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
  await flush();
}

async function enter() {
  return pressKey(combobox()!, { key: 'Enter', code: 'Enter' });
}

beforeEach(() => {
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  closePalette();
  closeSessions();
  argumentItem.value = null;
  setCinematic(false);
  setMotion('full');
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('the command palette', () => {
  it('opens on Ctrl+K and on Cmd+K, even from the prompt', async () => {
    seed();
    stubSources();
    await mount();

    const e = await openWithKey();
    expect(e.defaultPrevented).toBe(true);
    expect(dialog()!.getAttribute('aria-modal')).toBe('true');
    expect(dialog()!.getAttribute('aria-label')).toBe('Command palette');
    expect(document.activeElement).toBe(combobox());

    await act(() => { closePalette(); });
    const meta = await openWithKey(true);
    expect(meta.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(combobox());
  });

  it('exposes the combobox state the list is reading', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    const box = combobox()!;
    expect(box.getAttribute('aria-expanded')).toBe('true');
    expect(box.getAttribute('aria-controls')).toBe('palette-list');
    const first = box.getAttribute('aria-activedescendant');
    expect(first).toBeTruthy();
    expect(root.querySelector(`#${first}`)!.getAttribute('aria-selected')).toBe('true');

    await pressKey(box, { key: 'ArrowDown', code: 'ArrowDown' });
    const next = box.getAttribute('aria-activedescendant');
    expect(next).not.toBe(first);
    expect(root.querySelector(`#${next}`)!.getAttribute('aria-selected')).toBe('true');
  });

  it('renders command titles as text', async () => {
    seed();
    stubSources({ commands: [{ id: 'x', title: '<img src=x onerror=alert(1)>', kind: 'custom', args: [] }] });
    await mount();
    await openWithKey();
    expect(dialog()!.querySelector('img')).toBeNull();
    expect(dialog()!.textContent).toContain('<img src=x onerror=alert(1)>');
  });

  it('runs an action on Enter, closes and returns the focus', async () => {
    seed();
    stubSources();
    const summarize = vi.spyOn(api, 'summarize').mockResolvedValue(undefined);
    await mount();
    deck().focus();
    await openWithKey();

    await type('compact');
    await enter();

    expect(summarize).toHaveBeenCalledWith('s1');
    expect(paletteOpen.value).toBeNull();
    expect(document.activeElement).toBe(deck());
  });

  it('opens the argument form for a command with arguments', async () => {
    seed();
    stubSources();
    const run = vi.spyOn(api, 'runCommand').mockResolvedValue(undefined);
    await mount();
    await openWithKey();

    await type('user:fix');
    await enter();

    const file = root.querySelector<HTMLInputElement>('#argument-FILE')!;
    expect(file).not.toBeNull();
    expect(document.activeElement).toBe(file);
    expect(dialog()!.querySelector('[role="combobox"]')).toBeNull();

    await act(() => {
      file.value = 'a.go';
      file.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => {
      root.querySelector('form.palette__form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    await flush();

    expect(run).toHaveBeenCalledWith('s1', 'user:fix', { FILE: 'a.go' });
    expect(paletteOpen.value).toBeNull();
  });

  it('attaches a skill to the deck instead of running it', async () => {
    seed();
    stubSources();
    const attach = vi.spyOn(api, 'attachSkill').mockResolvedValue({
      attachment: { name: 'go-tests.md', mime_type: 'text/markdown', content: '# x' },
    });
    const run = vi.spyOn(api, 'runCommand');
    await mount();
    await openWithKey();

    await type('go-tests');
    await enter();

    expect(attach).toHaveBeenCalledWith('go-tests');
    expect(run).not.toHaveBeenCalled();
    expect(root.querySelector('.deck__files')!.textContent).toContain('go-tests.md');
    expect(paletteOpen.value).toBeNull();
  });

  it('shows a starting state for agent_starting and an alert for agent_busy', async () => {
    seed();
    stubSources();
    vi.spyOn(api, 'summarize')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockRejectedValueOnce(new ApiError(409, 'agent_busy', 'Crush is busy right now'));
    await mount();
    await openWithKey();

    await type('compact');
    await enter();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    expect(status.getAttribute('data-tone')).toBe('starting');
    expect(paletteOpen.value).toBe('all');

    await enter();
    const alert = dialog()!.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe('Crush is busy right now');
    expect(alert.getAttribute('data-tone')).toBe('error');
  });

  it('closes on Escape before the inspector or the cinematic view', async () => {
    seed({ inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false } });
    stubSources();
    await mount();
    await openWithKey();

    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(paletteOpen.value).toBeNull();
    expect(state.value.inspected?.sessionId).toBe('sub-1');
  });

  it('is inert while a permission request is open', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    const layer = root.querySelector('.palette-layer')!;
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

  it('lists the sessions and follows the picked one', async () => {
    seed();
    stubSources();
    const follow = vi.spyOn(api, 'follow').mockResolvedValue(undefined);
    await mount();

    await act(() => { openPalette('sessions'); });
    await flush();
    expect(dialog()!.textContent).toContain('Fix the lexer');
    await enter();

    expect(follow).toHaveBeenCalledWith('s-2');
    expect(paletteOpen.value).toBeNull();
  });

  it('opens the session switcher from the Sessions action', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('sessions');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(sessionsOpen.value).toBe(true);
    expect(root.querySelector('[role="dialog"][aria-label="Sessions"]')).not.toBeNull();
  });

  it('keeps the remaining groups when one source fails, with a Retry', async () => {
    seed();
    stubSources();
    vi.spyOn(api, 'sessions')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce([session('s-2', 'Fix the lexer')]);
    await mount();
    await openWithKey();

    expect(dialog()!.textContent).toContain('New session');
    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    const retry = [...dialog()!.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!;
    await act(() => { retry.click(); });
    await flush();

    expect(dialog()!.textContent).toContain('Fix the lexer');
    expect(dialog()!.querySelector('[role="status"]')).toBeNull();
  });

  it('picks a model through the provider choices', async () => {
    seed();
    stubSources({
      models: [{ provider: 'p1', provider_name: 'Provider One', id: 'm2', name: 'Model Two', context_window: 1000, can_reason: false }],
    });
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await mount();

    await act(() => { openPalette('models'); });
    await flush();
    await enter();

    expect(select).toHaveBeenCalledWith('main', 'p1', 'm2');
    expect(paletteOpen.value).toBeNull();
  });

  it('opens the Display menu for Display settings', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('display');
    await enter();

    const menu = root.querySelector<HTMLDetailsElement>('details.motion-menu')!;
    expect(menu.open).toBe(true);
    expect(paletteOpen.value).toBeNull();
    expect(document.activeElement).toBe(menu.querySelector('summary'));
  });

  it('hands the list back from the argument form on Escape', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('user:fix');
    await enter();
    expect(root.querySelector('#argument-FILE')).not.toBeNull();

    await pressKey(root.querySelector('#argument-FILE')!, { key: 'Escape', code: 'Escape' });

    expect(paletteOpen.value).toBe('all');
    expect(argumentItem.value).toBeNull();
    expect(document.activeElement).toBe(combobox());
  });

  it('starts a new session and adopts it', async () => {
    seed({ sessionId: '' });
    stubSources();
    const create = vi.spyOn(api, 'createSession').mockResolvedValue(session('s-9', 'New session'));
    await mount();
    await openWithKey();

    await type('new session');
    await enter();
    await flush();

    expect(create).toHaveBeenCalledTimes(1);
    expect(state.value.sessionId).toBe('s-9');
    expect(paletteOpen.value).toBeNull();
  });

  it('opens the skills section from Toggle skills', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('toggle skills');
    await enter();
    await flush();

    expect(paletteOpen.value).toBe('skills');
    expect(dialog()!.textContent).toContain('Enabled');
  });

  it('toggles thinking through the reasoning route', async () => {
    seed({ model: { ...state.value.model, can_reason: true, effort_levels: [], think: false } });
    stubSources();
    const reasoning = vi.spyOn(api, 'setReasoning').mockResolvedValue(state.value.model);
    await mount();
    await openWithKey();

    await type('enable thinking');
    await enter();

    expect(reasoning).toHaveBeenCalledWith({ think: true });
    expect(paletteOpen.value).toBeNull();
  });

  it('toggles a skill in place from the skills section', async () => {
    seed();
    stubSources({ skills: [{ name: 'go-tests', description: 'Write Go tests', disabled: false }] });
    const disable = vi.spyOn(api, 'setSkillDisabled').mockResolvedValue(undefined);
    await mount();

    await act(() => { openPalette('skills'); });
    await flush();
    expect(dialog()!.textContent).toContain('Enabled');
    await enter();

    expect(disable).toHaveBeenCalledWith('go-tests', true);
    expect(paletteOpen.value).toBe('skills');
  });

  it('opens with the typed slash from an empty deck and keeps it out of the text', async () => {
    seed();
    stubSources();
    await mount();
    deck().focus();

    const slash = await pressKey(deck(), { key: '/', code: 'Slash' });

    expect(slash.defaultPrevented).toBe(true);
    expect(paletteOpen.value).toBe('all');
    expect(paletteQuery.value).toBe('');
    expect(deck().value).toBe('');
    expect(document.activeElement).toBe(combobox());
  });

  it('retries only the sources that failed', async () => {
    seed();
    stubSources();
    vi.mocked(api.sessions).mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'));
    await mount();
    await openWithKey();

    expect(vi.mocked(api.sessions)).toHaveBeenCalledTimes(1);
    expect(dialog()!.textContent).toContain('New session');
    const retry = retryButton();
    expect(retry).not.toBeNull();
    await act(() => { retry!.click(); });
    await flush();

    expect(vi.mocked(api.sessions)).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.commands)).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.mcpPrompts)).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.skills)).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.providers)).toHaveBeenCalledTimes(1);
    expect(dialog()!.textContent).toContain('Fix the lexer');
    expect(dialog()!.textContent).toContain('New session');
    expect(dialog()!.textContent).toContain('go-tests');
    expect(retryButton()).toBeNull();
  });

  it('keeps the old skills when the refetch after a toggle fails', async () => {
    seed();
    stubSources({ skills: [{ name: 'go-tests', description: 'Write Go tests', disabled: false }] });
    const disable = vi.spyOn(api, 'setSkillDisabled').mockResolvedValue(undefined);
    await mount();
    await act(() => { openPalette('skills'); });
    await flush();
    vi.mocked(api.skills).mockRejectedValueOnce(new ApiError(500, 'internal', 'boom'));

    await enter();

    expect(disable).toHaveBeenCalledWith('go-tests', true);
    expect(paletteOpen.value).toBe('skills');
    expect(dialog()!.textContent).toContain('go-tests');
    expect(dialog()!.textContent).toContain('Enabled');
    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toContain('boom');
    expect(vi.mocked(api.commands)).toHaveBeenCalledTimes(1);
  });

  it('hides Retry while an action failure is the one on the line', async () => {
    seed();
    stubSources();
    vi.mocked(api.sessions).mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'));
    vi.spyOn(api, 'summarize').mockRejectedValueOnce(new ApiError(409, 'agent_busy', 'Crush is busy right now'));
    await mount();
    await openWithKey();

    expect(retryButton()).not.toBeNull();

    await type('compact');
    await enter();

    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toBe('Crush is busy right now');
    expect(retryButton()).toBeNull();
  });

  it('clears a stale action failure on the next attempt', async () => {
    seed();
    stubSources();
    vi.spyOn(api, 'summarize').mockRejectedValueOnce(new ApiError(409, 'agent_busy', 'Crush is busy right now'));
    await mount();
    await openWithKey();

    await type('compact');
    await enter();
    expect(dialog()!.querySelector('[role="alert"]')).not.toBeNull();

    await type('toggle skills');
    await enter();

    expect(dialog()!.querySelector('[role="alert"]')).toBeNull();
    expect(dialog()!.querySelector('[role="status"]')).toBeNull();
  });

  it('ignores a held Ctrl+K', async () => {
    seed();
    stubSources();
    await mount();
    deck().focus();

    const held = await pressKey(deck(), { key: 'k', code: 'KeyK', ctrlKey: true, repeat: true });

    expect(held.defaultPrevented).toBe(false);
    expect(paletteOpen.value).toBeNull();

    await pressKey(deck(), { key: 'k', code: 'KeyK', ctrlKey: true });
    expect(paletteOpen.value).toBe('all');
  });

  it('reports the combobox expanded only while there are matches', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    expect(combobox()!.getAttribute('aria-expanded')).toBe('true');
    await type('zzz no such command');

    expect(dialog()!.textContent).toContain('No matches');
    expect(combobox()!.getAttribute('aria-expanded')).toBe('false');
  });

  it('scrolls the active option into view as the arrows move it', async () => {
    seed();
    stubSources();
    await mount();
    const proto = Element.prototype as unknown as { scrollIntoView?: (arg?: unknown) => void };
    const before = proto.scrollIntoView;
    const scroll = vi.fn();
    proto.scrollIntoView = scroll;
    try {
      await openWithKey();
      expect(scroll.mock.instances.at(-1)).toBe(root.querySelector('#palette-option-0'));

      await pressKey(combobox()!, { key: 'ArrowDown', code: 'ArrowDown' });

      expect(scroll).toHaveBeenLastCalledWith({ block: 'nearest' });
      expect(scroll.mock.instances.at(-1)).toBe(root.querySelector('#palette-option-1'));
    } finally {
      proto.scrollIntoView = before;
    }
  });
});

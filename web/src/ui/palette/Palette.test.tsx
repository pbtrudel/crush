import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type {
  CommandEntry, Frame, McpPrompt, ModelChoice, PermissionRequest, Session, SkillEntry,
} from '../../api/types';
import { ApiError, api } from '../../api/http';
import { initialState, reduce } from '../../state/reducer';
import { sideAnswer, state } from '../../state/store';
import {
  argumentItem, closeModels, closePalette, closeSessions, closeSystems, deckMode, modelsOpen, openPalette,
  paletteOpen, paletteQuery, sessionsOpen, systemsOpen,
} from '../../state/palette';
import { setCinematic } from '../../state/cinematic';
import { closeSignIn, signInOpen } from '../../state/signin';
import { motion, setMotion } from '../../state/motion';
import { closeDisplay, displayOpen } from '../../state/display';
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => { resolve = res; });
  return { promise, resolve };
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
  closeModels();
  closeSystems();
  closeSignIn();
  closeDisplay();
  argumentItem.value = null;
  deckMode.value = null;
  sideAnswer.value = null;
  vi.unstubAllGlobals();
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

    const status = dialog()!.querySelector('.palette__status')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    expect(status.getAttribute('data-tone')).toBe('starting');
    expect(paletteOpen.value).toBe('all');

    await enter();
    const alert = dialog()!.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe('Crush is busy right now');
    expect(alert.getAttribute('data-tone')).toBe('error');
  });

  it('retries an action the starting agent refused', async () => {
    seed();
    stubSources();
    const summarize = vi.spyOn(api, 'summarize')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce(undefined);
    await mount();
    await openWithKey();

    await type('compact');
    await enter();

    const retry = retryButton();
    expect(retry).not.toBeNull();
    await act(async () => { retry!.click(); });
    await flush();

    expect(summarize).toHaveBeenCalledTimes(2);
    expect(summarize).toHaveBeenLastCalledWith('s1');
    expect(paletteOpen.value).toBeNull();
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

  it('keeps the focus in the panel when a failed source stops failing', async () => {
    seed();
    stubSources();
    vi.mocked(api.sessions).mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'));
    await mount();
    await openWithKey();

    // The Retry refetches only the source that failed, so a successful one
    // takes its line away while it holds the focus.
    const retry = retryButton()!;
    retry.focus();
    await act(() => { retry.click(); });
    await flush();

    expect(retryButton()).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(combobox());

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
    expect(root.querySelector('.palette-layer')!.hasAttribute('inert')).toBe(true);
    expect(panel.contains(document.activeElement)).toBe(false);

    // A prompt that still needs the user owns the focus, so a later render
    // of the panel under it does not take it back.
    await act(() => { state.value = { ...state.value, permissions: [prompt, { ...prompt, id: 'p2' }] }; });
    await flush();
    expect(panel.contains(document.activeElement)).toBe(false);

    // The prompt closes: the panel takes the control that had the focus back.
    await act(() => { state.value = { ...state.value, permissions: [] }; });
    await flush();
    expect(document.activeElement).toBe(combobox());
  });

  it('gives the combobox its focus back when a clicked item fails', async () => {
    seed();
    stubSources();
    vi.spyOn(api, 'summarize').mockRejectedValue(new ApiError(409, 'agent_busy', 'Crush is busy right now'));
    await mount();
    await openWithKey();

    await type('compact');
    const row = Array.from(dialog()!.querySelectorAll<HTMLElement>('[role="option"]'))
      .find((o) => o.textContent?.includes('Compact'))!;
    // A click does not move the focus in jsdom, so the dialog keeps it and
    // the fix is the only thing that hands it to the combobox.
    dialog()!.focus();
    await act(() => { row.click(); });
    await flush();

    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toBe('Crush is busy right now');
    expect(document.activeElement).toBe(combobox());
  });

  it('drops an older source answer that a newer load replaced', async () => {
    seed();
    stubSources();
    const older = deferred<Session[]>();
    const newer = deferred<Session[]>();
    vi.mocked(api.sessions)
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    await mount();
    await openWithKey();

    await act(() => { retryButton()!.click(); });
    await flush();
    // The line stays until an answer lands, so a second Retry starts a
    // newer load of the same source.
    await act(() => { retryButton()!.click(); });
    await flush();

    await act(async () => { newer.resolve([session('s-2', 'Newer answer')]); await Promise.resolve(); });
    await flush();
    await act(async () => { older.resolve([session('s-2', 'Older answer')]); await Promise.resolve(); });
    await flush();

    expect(dialog()!.textContent).toContain('Newer answer');
    expect(dialog()!.textContent).not.toContain('Older answer');
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

  it('returns the focus to the deck when a palette-opened dialog closes', async () => {
    seed();
    stubSources();
    await mount();
    // Ctrl+K from a page with no focused control leaves the palette with the
    // body as its opener, which is not a useful place to hand focus back to.
    await pressKey(document.body, { key: 'k', code: 'KeyK', ctrlKey: true });
    expect(paletteOpen.value).toBe('all');

    await type('sessions');
    await enter();
    expect(sessionsOpen.value).toBe(true);

    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(sessionsOpen.value).toBe(false);
    expect(document.activeElement).toBe(deck());
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
    const status = dialog()!.querySelector('.palette__status')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    const retry = [...dialog()!.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!;
    await act(() => { retry.click(); });
    await flush();

    expect(dialog()!.textContent).toContain('Fix the lexer');
    expect(dialog()!.querySelector('.palette__status')).toBeNull();
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

  it('opens the model selector from Switch model', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('switch model');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(modelsOpen.value).toBe(true);
    expect(root.querySelector('[role="dialog"][aria-label="Models"]')).not.toBeNull();
  });

  it('opens the model selector at the reasoning control', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('reasoning level');
    await enter();

    expect(modelsOpen.value).toBe(true);
    const checked = root.querySelector<HTMLInputElement>('input[name="model-effort"]:checked');
    expect(checked).not.toBeNull();
    expect(document.activeElement).toBe(checked);
  });

  it('opens the systems panel from Systems', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('systems');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(systemsOpen.value).toBe('all');
    expect(root.querySelector('[role="dialog"][aria-label="Systems"]')).not.toBeNull();
  });

  it('opens the sign-in dialog from Sign in to a provider', async () => {
    seed();
    stubSources();
    vi.spyOn(api, 'signInTargets').mockResolvedValue({ providers: [] });
    vi.spyOn(api, 'signInStatus').mockRejectedValue(new ApiError(404, 'no_flow', 'no sign-in has started'));
    await mount();
    await openWithKey();

    await type('sign in');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(signInOpen.value).not.toBeNull();
    expect(root.querySelector('[role="dialog"][aria-label="Sign in"]')).not.toBeNull();
  });

  it('puts a bang in the deck for Run shell command', async () => {
    seed();
    stubSources();
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0; });
    await mount();
    deck().focus();
    await openWithKey();

    await type('run shell');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(deck().value).toBe('!');
    expect(root.querySelector('.deck__mode-badge')!.textContent).toBe('Shell');
    expect(document.activeElement).toBe(deck());
  });

  it('turns the deck to note mode for Add note', async () => {
    seed();
    stubSources();
    await mount();
    deck().focus();
    await openWithKey();

    await type('add note');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(deckMode.value).toBe('note');
    expect(root.querySelector('.deck__mode-badge')!.textContent).toBe('Note');
    expect(document.activeElement).toBe(deck());
  });

  it('asks a side question through the argument form', async () => {
    seed();
    stubSources();
    const ask = vi.spyOn(api, 'ask').mockResolvedValue({ answer: 'Because the tests say so.' });
    await mount();
    deck().focus();
    await openWithKey();

    await type('ask side');
    await enter();

    const form = root.querySelector<HTMLFormElement>('form.palette__form')!;
    expect(form.getAttribute('aria-label')).toBe('Arguments for Ask side question');
    expect(root.querySelector('[role="combobox"]')).toBeNull();
    const question = root.querySelector<HTMLInputElement>('#argument-question')!;
    expect(question).not.toBeNull();
    expect(document.activeElement).toBe(question);

    await act(() => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
    expect(form.querySelector('.palette__error')!.textContent).toBe('Question is required.');
    expect(ask).not.toHaveBeenCalled();

    await act(() => {
      question.value = 'Why the lexer?';
      question.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
    await flush();

    expect(ask).toHaveBeenCalledWith('s1', 'Why the lexer?');
    expect(paletteOpen.value).toBeNull();
    expect(sideAnswer.value?.question).toBe('Why the lexer?');
    const card = root.querySelector('.side-answer')!;
    expect(card.textContent).toContain('Side answer, not added to the conversation');
    expect(card.textContent).toContain('Because the tests say so.');
    expect(document.activeElement).toBe(deck());
  });

  it('opens the display settings dialog for Display settings', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    await type('display');
    await enter();

    expect(paletteOpen.value).toBeNull();
    expect(displayOpen.value).toBe(true);
    const dialog = root.querySelector<HTMLElement>('[role="dialog"][aria-label="Display settings"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.contains(document.activeElement)).toBe(true);

    // Pins the deck return: useDialogFocus's own restore already lands on
    // the deck when the palette opened from it.
    await pressKey(dialog!.querySelector('input')!, { key: 'Escape', code: 'Escape' });
    expect(displayOpen.value).toBe(false);
    expect(document.activeElement).toBe(deck());
  });

  it('writes the config from the palette motion cycle', async () => {
    seed();
    stubSources();
    const set = vi.spyOn(api, 'setConfig').mockResolvedValue({ ...state.value.display, motion: 'reduced' });
    await mount();
    await openWithKey();

    await type('effects');
    await enter();

    expect(set).toHaveBeenCalledWith('options.web_display.motion', 'reduced');
    expect(paletteOpen.value).toBeNull();
    expect(state.value.display.motion).toBe('reduced');
    expect(motion.value).toBe('reduced');
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

  it('creates one session when Enter fires twice while the first is in flight', async () => {
    seed({ sessionId: '' });
    stubSources();
    const answer = deferred<Session>();
    const create = vi.spyOn(api, 'createSession').mockReturnValue(answer.promise);
    await mount();
    await openWithKey();

    await type('new session');
    await enter();
    await enter();

    expect(create).toHaveBeenCalledTimes(1);

    await act(async () => { answer.resolve(session('s-9', 'New session')); await Promise.resolve(); });
    await flush();
    expect(state.value.sessionId).toBe('s-9');
    expect(paletteOpen.value).toBeNull();
  });

  it('runs one command when the argument form submits twice in flight', async () => {
    seed();
    stubSources();
    const answer = deferred<void>();
    const run = vi.spyOn(api, 'runCommand').mockReturnValue(answer.promise);
    await mount();
    await openWithKey();

    await type('user:fix');
    await enter();
    const file = root.querySelector<HTMLInputElement>('#argument-FILE')!;
    await act(() => {
      file.value = 'a.go';
      file.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => {
      const form = root.querySelector('form.palette__form')!;
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });

    expect(run).toHaveBeenCalledTimes(1);

    await act(async () => { answer.resolve(undefined); await Promise.resolve(); });
    await flush();
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

  it('moves the focus to the list before the retry leaves', async () => {
    seed();
    stubSources();
    const answer = deferred<void>();
    vi.spyOn(api, 'summarize')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockReturnValueOnce(answer.promise);
    await mount();
    await openWithKey();

    await type('compact');
    await enter();

    const retry = retryButton()!;
    retry.focus();
    expect(document.activeElement).toBe(retry);
    await act(() => { retry.click(); });
    await flush();

    expect(document.activeElement).toBe(combobox());

    await act(async () => { answer.resolve(undefined); await Promise.resolve(); });
    await flush();
  });

  it('hides Retry while an error-tone action failure is the one on the line', async () => {
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
    expect(dialog()!.querySelector('.palette__status')).toBeNull();
    expect(dialog()!.querySelector('.palette__empty')!.textContent).toBe('');
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

  it('keeps an empty status mounted before any query', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    const status = dialog()!.querySelector('.palette__empty')!;
    expect(status).not.toBeNull();
    expect(status.getAttribute('role')).toBe('status');
    expect(status.textContent).toBe('');
    expect(dialog()!.querySelector('[role="listbox"]')!.contains(status)).toBe(false);
  });

  it('keeps an empty answer out of the listbox and lets the dialog take focus', async () => {
    seed();
    stubSources();
    await mount();
    await openWithKey();

    expect(dialog()!.getAttribute('tabindex')).toBe('-1');

    await type('zzz no such command');

    const listbox = dialog()!.querySelector('[role="listbox"]')!;
    expect(listbox.textContent).toBe('');
    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toBe('No matches');
    expect(listbox.contains(status)).toBe(false);
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

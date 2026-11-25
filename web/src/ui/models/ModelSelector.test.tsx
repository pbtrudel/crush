import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame, ModelChoice, ModelInfo, PermissionRequest } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { initialState, reduce } from '../../state/reducer';
import { dispatch, state } from '../../state/store';
import { closeModels, closePalette, modelsOpen, openModels } from '../../state/palette';
import { resetWebglProbe } from '../../state/effects';
import { setCinematic } from '../../state/cinematic';
import { closeSignIn, signInOpen } from '../../state/signin';
import { App } from '../App';
import { formatContext } from './ModelSelector';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const originalGetContext = HTMLCanvasElement.prototype.getContext;

function choice(patch: Partial<ModelChoice> & Pick<ModelChoice, 'provider' | 'id' | 'name'>): ModelChoice {
  return {
    provider_name: patch.provider === 'anthropic' ? 'Anthropic' : patch.provider === 'p2' ? 'Provider Two' : 'Provider One',
    context_window: 200_000,
    can_reason: false,
    ...patch,
  };
}

const CHOICES: ModelChoice[] = [
  choice({ provider: 'anthropic', id: 'claude', name: 'Claude' }),
  choice({ provider: 'anthropic', id: 'claude-haiku', name: 'Claude Haiku' }),
  choice({ provider: 'p1', id: 'm1', name: 'Model One' }),
  choice({ provider: 'p1', id: 'm2', name: 'Model Two', context_window: 128_000 }),
  choice({ provider: 'p2', id: 'm3', name: 'Model Three', context_window: 1_500_000 }),
];

function seed(patch: Partial<ModelInfo> = {}, more: Partial<typeof state.value> = {}) {
  const base = reduce(initialState(), fx.snapshot);
  state.value = { ...base, conn: 'live', model: { ...base.model, ...patch }, ...more };
}

function stubProviders(choices: ModelChoice[] = CHOICES) {
  return vi.spyOn(api, 'providers').mockResolvedValue({ choices });
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

function hudButton() {
  return root.querySelector<HTMLButtonElement>('p.hud__meta button.hud__model')!;
}

function hudModel() {
  return hudButton().textContent;
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Models"]');
}

function filter() {
  return root.querySelector<HTMLInputElement>('input[aria-label="Filter models"]')!;
}

function options(): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('.models__option'));
}

function option(name: string): HTMLElement {
  return options().find((o) => o.querySelector('.models__name')?.textContent === name)!;
}

function radio(name: string, value: string) {
  return root.querySelector<HTMLInputElement>(`input[name="${name}"][value="${value}"]`)!;
}

function thinkingSwitch() {
  return root.querySelector<HTMLButtonElement>('[role="switch"]');
}

function groups() {
  return Array.from(root.querySelectorAll<HTMLElement>('.models__group'));
}

async function openFromHud() {
  const button = hudButton();
  button.focus();
  await act(() => { button.click(); });
  await flush();
}

async function pressKey(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
  await act(() => { target.dispatchEvent(e); });
  await flush();
  return e;
}

async function type(value: string) {
  const input = filter();
  await act(() => {
    input.value = value;
    input.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
  await flush();
}

async function frame(payload: Partial<ModelInfo>) {
  await act(() => {
    dispatch((s) => reduce(s, { seq: 90, type: 'model_changed', payload: { ...s.model, ...payload } as unknown as ModelInfo }));
  });
  await flush();
}

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof originalGetContext;
  resetWebglProbe();
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  closeModels();
  closePalette();
  closeSignIn();
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = originalGetContext;
  resetWebglProbe();
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('the model selector', () => {
  it('opens from the HUD model name', async () => {
    seed();
    stubProviders();
    await mount();

    expect(hudButton().getAttribute('aria-label')).toBe('Model: Claude. Switch model');
    expect(hudModel()).toBe('Claude');
    await openFromHud();

    expect(api.providers).toHaveBeenCalledTimes(1);
    expect(dialog()).not.toBeNull();
    expect(document.activeElement).toBe(filter());
  });

  it('lists choices grouped by provider and marks the current model', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    expect(groups().map((g) => g.querySelector('.models__provider')!.textContent))
      .toEqual(['Anthropic', 'Provider One', 'Provider Two']);
    expect(options().map((o) => o.querySelector('.models__name')!.textContent))
      .toEqual(['Claude', 'Claude Haiku', 'Model One', 'Model Two', 'Model Three']);
    expect(options().map((o) => o.querySelector('.models__context')!.textContent))
      .toEqual(['200k', '200k', '200k', '128k', '1.5M']);

    // The marker is the last thing a reader hears, and the row's look hangs
    // on the data attribute instead of the selected state.
    expect(option('Claude').hasAttribute('aria-selected')).toBe(false);
    expect(option('Claude').getAttribute('data-current')).toBe('true');
    expect(option('Claude').textContent).toMatch(/, current$/);
    expect(option('Model One').hasAttribute('aria-selected')).toBe(false);
    expect(option('Model One').getAttribute('data-current')).toBeNull();
    expect(option('Model One').textContent).not.toContain('current');

    expect(dialog()!.textContent).toContain('Sign in to another provider');
  });

  it('opens the sign-in dialog from its footer and closes the selector', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'signInTargets').mockResolvedValue({ providers: [] });
    vi.spyOn(api, 'signInStatus').mockRejectedValue(new ApiError(404, 'no_flow', 'no sign-in has started'));
    await mount();
    await openFromHud();

    const footer = Array.from(root.querySelectorAll('button'))
      .find((b) => b.textContent === 'Sign in to another provider')!;
    await act(() => { footer.click(); });
    await flush();

    expect(modelsOpen.value).toBe(false);
    expect(signInOpen.value).toEqual({ provider: undefined });
    expect(root.querySelector('[role="dialog"][aria-label="Sign in"]')).not.toBeNull();
  });

  it('keeps provider headings out of the listbox children axe checks', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    // A listbox may own only options and groups, so each group carries the
    // provider name and the visible heading is decorative.
    const list = root.querySelector('[role="listbox"][aria-label="Model choices"]')!;
    const providers = Array.from(list.children).filter((el) => el.getAttribute('role') === 'group');
    expect(providers.map((g) => g.getAttribute('aria-label')))
      .toEqual(['Anthropic', 'Provider One', 'Provider Two']);
    for (const heading of list.querySelectorAll('.models__provider')) {
      expect(heading.getAttribute('aria-hidden')).toBe('true');
    }
  });

  it('filters the choices as you type', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    await type('model two');
    expect(options().map((o) => o.querySelector('.models__name')!.textContent)).toEqual(['Model Two']);

    // A provider name matches its whole group.
    await type('provider two');
    expect(options().map((o) => o.querySelector('.models__name')!.textContent)).toEqual(['Model Three']);

    await type('nothing');
    expect(options()).toEqual([]);
    expect(dialog()!.textContent).toContain('No models match');

    await type('');
    expect(options().length).toBe(CHOICES.length);
  });

  it('selects a model and closes, and the HUD waits for the frame', async () => {
    seed();
    stubProviders();
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    expect(select).toHaveBeenCalledWith('main', 'p1', 'm2');
    expect(modelsOpen.value).toBe(false);
    expect(dialog()).toBeNull();
    // The answer alone must not move the HUD: only the frame does.
    expect(hudModel()).toBe('Claude');

    await frame({ id: 'm2', name: 'Model Two', provider: 'p1' });
    expect(hudModel()).toBe('Model Two');
  });

  it('closes without a request on the current model of the chosen type', async () => {
    seed();
    stubProviders();
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await mount();
    await openFromHud();

    await act(() => { option('Claude').click(); });
    await flush();

    expect(select).not.toHaveBeenCalled();
    expect(modelsOpen.value).toBe(false);
  });

  it('switches the model type to the small model', async () => {
    seed({ small: { id: 'm2', provider: 'p1' } });
    stubProviders();
    const select = vi.spyOn(api, 'selectModel').mockResolvedValue(state.value.model);
    await mount();
    await openFromHud();

    await act(() => { radio('model-type', 'small').click(); });
    await flush();

    // The current mark follows state.model.small.
    expect(option('Model Two').getAttribute('data-current')).toBe('true');
    expect(option('Model Two').textContent).toContain('current');
    expect(option('Claude').getAttribute('data-current')).toBeNull();

    await act(() => { option('Model One').click(); });
    await flush();

    expect(select).toHaveBeenCalledWith('small', 'p1', 'm1');
    expect(modelsOpen.value).toBe(false);
  });

  it('shows the effort levels of a reasoning model and sets the chosen one', async () => {
    seed();
    stubProviders();
    const reasoning = vi.spyOn(api, 'setReasoning').mockResolvedValue(state.value.model);
    await mount();
    await openFromHud();

    const fieldset = dialog()!.querySelector('fieldset.models__reasoning')!;
    expect(fieldset.querySelector('legend')!.textContent).toBe('Reasoning');
    expect(Array.from(fieldset.querySelectorAll<HTMLInputElement>('input[name="model-effort"]')).map((r) => r.value))
      .toEqual(['low', 'medium', 'high']);
    expect(radio('model-effort', 'high').checked).toBe(true);

    await act(() => { radio('model-effort', 'low').click(); });
    await flush();

    expect(reasoning).toHaveBeenCalledWith({ effort: 'low' });
    expect(modelsOpen.value).toBe(true);

    await frame({ reasoning_effort: 'low' });
    expect(radio('model-effort', 'low').checked).toBe(true);
    expect(radio('model-effort', 'high').checked).toBe(false);
  });

  it('shows a thinking switch for a model without effort levels', async () => {
    seed({ effort_levels: undefined, reasoning_effort: undefined, think: false });
    stubProviders();
    const reasoning = vi.spyOn(api, 'setReasoning').mockResolvedValue(state.value.model);
    await mount();
    await openFromHud();

    const control = thinkingSwitch()!;
    expect(control.textContent).toBe('Thinking');
    expect(control.getAttribute('aria-checked')).toBe('false');

    await act(() => { control.click(); });
    await flush();

    expect(reasoning).toHaveBeenCalledWith({ think: true });
    expect(modelsOpen.value).toBe(true);
  });

  it('shows no reasoning control for a model that cannot reason', async () => {
    seed({ can_reason: false, effort_levels: undefined, think: undefined });
    stubProviders();
    await mount();
    await openFromHud();

    expect(dialog()!.querySelector('fieldset.models__reasoning')).toBeNull();
    expect(thinkingSwitch()).toBeNull();
  });

  it('keeps the reasoning controls focusable while a change is in flight', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    const reasoning = vi.spyOn(api, 'setReasoning').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    const low = radio('model-effort', 'low');
    low.focus();
    await act(() => { low.click(); });
    await flush();

    const fieldset = dialog()!.querySelector<HTMLFieldSetElement>('fieldset.models__reasoning')!;
    expect(reasoning).toHaveBeenCalledWith({ effort: 'low' });
    // A disabled fieldset takes the focus with it, so it stays enabled.
    expect(fieldset.matches(':disabled')).toBe(false);
    expect(low.matches(':disabled')).toBe(false);
    expect(fieldset.getAttribute('aria-busy')).toBe('true');
    expect(fieldset.classList.contains('models__reasoning--busy')).toBe(true);
    expect(document.activeElement).toBe(low);

    await act(async () => { answer.resolve(state.value.model); await Promise.resolve(); });
    await flush();

    expect(fieldset.getAttribute('aria-busy')).toBeNull();
    expect(fieldset.classList.contains('models__reasoning--busy')).toBe(false);
    expect(document.activeElement).toBe(low);
  });

  it('keeps focus on the thinking switch while a change is in flight', async () => {
    seed({ effort_levels: undefined, reasoning_effort: undefined, think: false });
    stubProviders();
    const answer = deferred<ModelInfo>();
    const reasoning = vi.spyOn(api, 'setReasoning').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    const control = thinkingSwitch()!;
    control.focus();
    await act(() => { control.click(); });
    await flush();

    expect(reasoning).toHaveBeenCalledWith({ think: true });
    expect(control.matches(':disabled')).toBe(false);
    expect(document.activeElement).toBe(control);

    await act(async () => { answer.resolve(state.value.model); await Promise.resolve(); });
    await flush();

    expect(document.activeElement).toBe(control);
  });

  it('ignores a second reasoning change while one is in flight', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    const reasoning = vi.spyOn(api, 'setReasoning').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    const low = radio('model-effort', 'low');
    low.focus();
    await act(() => { low.click(); });
    await flush();

    const fieldset = dialog()!.querySelector<HTMLFieldSetElement>('fieldset.models__reasoning')!;
    expect(fieldset.matches(':disabled')).toBe(false);
    const medium = radio('model-effort', 'medium');
    await act(() => { medium.click(); });
    await flush();

    expect(reasoning).toHaveBeenCalledTimes(1);
    expect(reasoning).toHaveBeenCalledWith({ effort: 'low' });
    // The ignored choice never shows: the pending one stays checked.
    expect(low.checked).toBe(true);
    expect(medium.checked).toBe(false);

    await act(async () => { answer.resolve(state.value.model); await Promise.resolve(); });
    await flush();

    expect(reasoning).toHaveBeenCalledTimes(1);
    expect(medium.checked).toBe(false);
    expect(radio('model-effort', 'high').checked).toBe(true);
  });

  it('keeps the chosen effort checked when the answer lands before the frame', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    vi.spyOn(api, 'setReasoning').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    await act(() => { radio('model-effort', 'low').click(); });
    await flush();
    expect(radio('model-effort', 'low').checked).toBe(true);

    // The answer names no effort, so the draft keeps the one that was asked
    // for instead of flicking back to the model's old level.
    await act(async () => {
      answer.resolve({ ...state.value.model, reasoning_effort: undefined });
      await Promise.resolve();
    });
    await flush();

    expect(radio('model-effort', 'low').checked).toBe(true);
    expect(radio('model-effort', 'high').checked).toBe(false);

    await frame({ reasoning_effort: 'low' });
    expect(radio('model-effort', 'low').checked).toBe(true);
    expect(radio('model-effort', 'high').checked).toBe(false);
  });

  it('shows the effort the answer settled on until the frame arrives', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    vi.spyOn(api, 'setReasoning').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    await act(() => { radio('model-effort', 'low').click(); });
    await flush();

    // The model took another level than the one clicked.
    await act(async () => {
      answer.resolve({ ...state.value.model, reasoning_effort: 'medium' });
      await Promise.resolve();
    });
    await flush();

    expect(radio('model-effort', 'medium').checked).toBe(true);
    expect(radio('model-effort', 'low').checked).toBe(false);
    expect(radio('model-effort', 'high').checked).toBe(false);

    await frame({ reasoning_effort: 'medium' });
    expect(radio('model-effort', 'medium').checked).toBe(true);
  });

  it('moves the focus to the filter when the chosen row is filtered out', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'selectModel')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockRejectedValueOnce(new ApiError(409, 'agent_busy', 'the agent is busy'));
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();
    // The query hides the refused row, so its Retry has no row to hand the
    // focus to.
    await type('Model One');
    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    await act(() => { retry.click(); });
    await flush();

    expect(document.activeElement).toBe(filter());
  });

  it('moves focus to the filter when the frame swaps the control', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    radio('model-effort', 'high').focus();
    await frame({ effort_levels: undefined, reasoning_effort: undefined, can_reason: true, think: false });

    expect(thinkingSwitch()).not.toBeNull();
    expect(dialog()!.querySelector('input[name="model-effort"]')).toBeNull();
    expect(document.activeElement).toBe(filter());
  });

  it('reverts the effort to the model state when the change fails', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'setReasoning').mockRejectedValue(new ApiError(400, 'bad_reasoning', 'the model does not offer that reasoning effort'));
    await mount();
    await openFromHud();

    await act(() => { radio('model-effort', 'low').click(); });
    await flush();

    expect(radio('model-effort', 'high').checked).toBe(true);
    expect(radio('model-effort', 'low').checked).toBe(false);
    expect(dialog()!.querySelector('[role="alert"]')!.textContent)
      .toContain('the model does not offer that reasoning effort');
    expect(modelsOpen.value).toBe(true);
  });

  it('explains a busy refusal and keeps the dialog open', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(409, 'agent_busy', 'the agent is busy'));
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const alert = dialog()!.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe('Crush is busy. Try again when the turn ends.');
    expect(modelsOpen.value).toBe(true);
  });

  it('shows the amber starting line and still lists the choices', async () => {
    seed({}, { agentReady: false });
    stubProviders();
    await mount();
    await openFromHud();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.getAttribute('data-tone')).toBe('starting');
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    expect(options().length).toBe(CHOICES.length);
  });

  it('keeps one starting line when a select is refused while the agent starts', async () => {
    seed({}, { agentReady: false });
    stubProviders();
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(503, 'agent_starting', 'not ready'));
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    expect(dialog()!.querySelectorAll('[role="status"]').length).toBe(1);
    expect(dialog()!.querySelector('[role="status"]')!.textContent)
      .toContain('Crush is starting the agent. Try again in a moment.');
    expect(modelsOpen.value).toBe(true);
  });

  it('shows the starting line when a select answers 503', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(503, 'agent_starting', 'not ready'));
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.getAttribute('data-tone')).toBe('starting');
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    expect(dialog()!.querySelector('[role="alert"]')).toBeNull();
    expect(modelsOpen.value).toBe(true);
  });

  it('moves the focus to the refused row before the retry leaves', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    vi.spyOn(api, 'selectModel')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockReturnValueOnce(answer.promise);
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    retry.focus();
    expect(document.activeElement).toBe(retry);
    await act(() => { retry.click(); });
    await flush();

    expect(document.activeElement).toBe(option('Model Two'));

    await act(async () => { answer.resolve(state.value.model); await Promise.resolve(); });
    await flush();
  });

  it('marks the list busy while a select is in flight', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    vi.spyOn(api, 'selectModel').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();
    const list = dialog()!.querySelector<HTMLElement>('[role="listbox"]')!;

    await act(() => { option('Model Two').click(); });
    await flush();
    expect(list.getAttribute('aria-busy')).toBe('true');

    await act(async () => { answer.reject(new ApiError(409, 'agent_busy', 'Crush is busy right now')); await Promise.resolve(); });
    await flush();
    expect(list.getAttribute('aria-busy')).toBeNull();
    expect(dialog()!.querySelector('[role="alert"]')!.textContent).toBe('Crush is busy. Try again when the turn ends.');
  });

  it('retries the refused pick from the top starting line', async () => {
    seed({}, { agentReady: false });
    stubProviders();
    const select = vi.spyOn(api, 'selectModel')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce(state.value.model);
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    const retry = Array.from(status.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    expect(retry).not.toBeUndefined();
    await act(() => { retry.click(); });
    await flush();

    expect(select).toHaveBeenCalledTimes(2);
    expect(select).toHaveBeenLastCalledWith('main', 'p1', 'm2');
    expect(modelsOpen.value).toBe(false);
  });

  it('retries the refused choice once the agent starts', async () => {
    seed();
    stubProviders();
    const select = vi.spyOn(api, 'selectModel')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce(state.value.model);
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    expect(retry).not.toBeUndefined();
    await act(() => { retry.click(); });
    await flush();

    expect(select).toHaveBeenCalledTimes(2);
    expect(select).toHaveBeenLastCalledWith('main', 'p1', 'm2');
    expect(modelsOpen.value).toBe(false);
  });

  it('names the listbox apart from the dialog', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    expect(dialog()!.getAttribute('aria-label')).toBe('Models');
    expect(dialog()!.querySelector('[role="listbox"]')!.getAttribute('aria-label')).toBe('Model choices');
  });

  it('shows a load failure with a Retry that focuses the filter', async () => {
    seed();
    const providers = vi.spyOn(api, 'providers')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce({ choices: CHOICES });
    await mount();
    await openFromHud();

    const status = dialog()!.querySelector('[role="status"]')!;
    expect(status.textContent).toContain('Crush is starting the agent. Try again in a moment.');
    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    await act(() => { retry.click(); });
    await flush();

    expect(providers).toHaveBeenCalledTimes(2);
    expect(dialog()!.querySelector('[role="status"]')).toBeNull();
    expect(options().length).toBe(CHOICES.length);
    expect(document.activeElement).toBe(filter());
  });

  it('selects once on a double Enter', async () => {
    seed();
    stubProviders();
    const answer = deferred<ModelInfo>();
    const select = vi.spyOn(api, 'selectModel').mockReturnValue(answer.promise);
    await mount();
    await openFromHud();

    const target = option('Model Two');
    await act(() => {
      target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
      target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    });
    await flush();

    expect(select).toHaveBeenCalledTimes(1);
    await act(async () => { answer.resolve(state.value.model); await Promise.resolve(); });
    await flush();
    expect(modelsOpen.value).toBe(false);
  });

  it('moves from the filter into the list and roves the tab stop', async () => {
    // Model Two is the current model and sits third in the list, so the tab
    // stop starts there instead of at the first option.
    seed({ id: 'm2', provider: 'p1' });
    stubProviders();
    await mount();
    await openFromHud();
    const proto = Element.prototype as unknown as { scrollIntoView?: (arg?: unknown) => void };
    const before = proto.scrollIntoView;
    const scroll = vi.fn();
    proto.scrollIntoView = scroll;
    try {
      filter().focus();
      await pressKey(filter(), { key: 'ArrowDown', code: 'ArrowDown' });
      expect(document.activeElement).toBe(option('Model Two'));
      expect(option('Model Two').tabIndex).toBe(0);
      expect(option('Model One').tabIndex).toBe(-1);

      await pressKey(option('Model Two'), { key: 'ArrowDown', code: 'ArrowDown' });
      expect(document.activeElement).toBe(option('Model Three'));
      expect(option('Model Three').tabIndex).toBe(0);
      expect(option('Model Two').tabIndex).toBe(-1);

      await pressKey(option('Model Three'), { key: 'ArrowUp', code: 'ArrowUp' });
      expect(document.activeElement).toBe(option('Model Two'));
      expect(scroll).toHaveBeenCalledWith({ block: 'nearest' });
    } finally {
      proto.scrollIntoView = before;
    }
  });

  it('renders a model name with markup as text', async () => {
    seed();
    const evil = '<img src=x onerror=alert(1)>';
    stubProviders([choice({ provider: 'p1', id: 'm9', name: evil })]);
    await mount();
    await openFromHud();

    expect(option(evil)).not.toBeNull();
    expect(option(evil).textContent).toContain(evil);
    expect(dialog()!.querySelector('.models__list img')).toBeNull();
  });

  it('drops an answer that arrives after the dialog closed', async () => {
    seed();
    const answer = deferred<{ choices: ModelChoice[] }>();
    const providers = vi.spyOn(api, 'providers')
      .mockReturnValueOnce(answer.promise)
      .mockResolvedValueOnce({ choices: [choice({ provider: 'p1', id: 'm1', name: 'Model One' })] });
    await mount();
    await act(() => { hudButton().click(); });
    await flush();
    expect(dialog()).not.toBeNull();

    await pressKey(window, { key: 'Escape', code: 'Escape' });
    expect(dialog()).toBeNull();
    await act(async () => { answer.resolve({ choices: CHOICES }); await Promise.resolve(); });
    await flush();
    expect(dialog()).toBeNull();

    await openFromHud();
    expect(options().map((o) => o.querySelector('.models__name')!.textContent)).toEqual(['Model One']);
    expect(providers).toHaveBeenCalledTimes(2);
  });

  it('drops an older answer that resolves after a newer load', async () => {
    seed();
    const older = deferred<{ choices: ModelChoice[] }>();
    const newer = deferred<{ choices: ModelChoice[] }>();
    const providers = vi.spyOn(api, 'providers')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    await mount();
    await openFromHud();

    const retry = () => Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    await act(() => { retry().click(); });
    await flush();
    await act(() => { retry().click(); });
    await flush();
    expect(providers).toHaveBeenCalledTimes(3);

    await act(async () => {
      newer.resolve({ choices: [choice({ provider: 'p1', id: 'm1', name: 'Model One' })] });
      await Promise.resolve();
    });
    await flush();
    expect(options().map((o) => o.querySelector('.models__name')!.textContent)).toEqual(['Model One']);

    await act(async () => { older.resolve({ choices: CHOICES }); await Promise.resolve(); });
    await flush();
    expect(options().map((o) => o.querySelector('.models__name')!.textContent)).toEqual(['Model One']);
  });

  it('keeps focus inside the dialog when the reasoning control goes away', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    radio('model-effort', 'high').focus();
    expect(document.activeElement).toBe(radio('model-effort', 'high'));

    await frame({ can_reason: false, effort_levels: undefined, think: undefined });

    expect(dialog()!.querySelector('fieldset.models__reasoning')).toBeNull();
    expect(dialog()!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(filter());
  });

  it('closes on Escape and gives focus back to the HUD model button', async () => {
    seed();
    stubProviders();
    seed({}, { inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false } });
    await mount();
    const button = hudButton();
    await openFromHud();

    await pressKey(window, { key: 'Escape', code: 'Escape' });

    expect(modelsOpen.value).toBe(false);
    expect(document.activeElement).toBe(button);
    // The selector takes Escape before the inspector.
    expect(state.value.inspected?.sessionId).toBe('sub-1');
  });

  it('gives way to the palette on Ctrl+K', async () => {
    seed();
    stubProviders();
    vi.spyOn(api, 'commands').mockResolvedValue({ commands: [] });
    vi.spyOn(api, 'mcpPrompts').mockResolvedValue({ prompts: [] });
    vi.spyOn(api, 'skills').mockResolvedValue({ skills: [] });
    vi.spyOn(api, 'sessions').mockResolvedValue([]);
    await mount();
    await openFromHud();

    await pressKey(window, { key: 'k', code: 'KeyK', ctrlKey: true });

    expect(modelsOpen.value).toBe(false);
    expect(root.querySelector('[aria-label="Command palette"]')).not.toBeNull();
  });

  it('opens where the palette asks it to', async () => {
    seed();
    stubProviders();
    await mount();
    await act(() => { openModels('reasoning'); });
    await flush();

    expect(dialog()).not.toBeNull();
    expect(document.activeElement).toBe(radio('model-effort', 'high'));
  });

  it('is inert while a permission request is open', async () => {
    seed();
    stubProviders();
    await mount();
    await openFromHud();

    const layer = root.querySelector('.models-layer')!;
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

  it('keeps the focus in the panel when the starting Retry leaves it', async () => {
    seed({}, { agentReady: false });
    stubProviders();
    vi.spyOn(api, 'selectModel').mockRejectedValue(new ApiError(503, 'agent_starting', 'not ready'));
    await mount();
    await openFromHud();

    await act(() => { option('Model Two').click(); });
    await flush();

    const retry = Array.from(dialog()!.querySelectorAll('button')).find((b) => b.textContent === 'Retry')!;
    retry.focus();
    expect(document.activeElement).toBe(retry);

    // The agent comes up: the starting line that holds the Retry goes away
    // and the action line under the list takes its place.
    await act(() => { state.value = { ...state.value, agentReady: true }; });
    await flush();

    expect(retry.isConnected).toBe(false);
    expect(document.activeElement).not.toBe(document.body);
    expect(dialog()!.contains(document.activeElement)).toBe(true);
    const remembered = document.activeElement as HTMLElement;

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
    expect(root.querySelector('.models-layer')!.hasAttribute('inert')).toBe(true);
    expect(panel.contains(document.activeElement)).toBe(false);

    // A prompt that still needs the user owns the focus, so a later render
    // of the panel under it does not take it back.
    await act(() => { state.value = { ...state.value, permissions: [prompt, { ...prompt, id: 'p2' }] }; });
    await flush();
    expect(panel.contains(document.activeElement)).toBe(false);

    // The prompt closes: the panel takes the control that had the focus back.
    await act(() => { state.value = { ...state.value, permissions: [] }; });
    await flush();
    expect(document.activeElement).toBe(remembered);
  });

  it('keeps the model button out of the tab order in cinematic mode', async () => {
    seed();
    setCinematic(true);
    await mount();

    expect(hudButton().tabIndex).toBe(-1);
    setCinematic(false);
    await act(() => { state.value = { ...state.value }; });
    expect(hudButton().tabIndex).toBe(0);
  });
});

describe('formatContext', () => {
  it('shows thousands under a million and millions above', () => {
    expect(formatContext(200_000)).toBe('200k');
    expect(formatContext(128_000)).toBe('128k');
    expect(formatContext(1_000_000)).toBe('1M');
    expect(formatContext(1_500_000)).toBe('1.5M');
    expect(formatContext(1_050_000)).toBe('1.1M');
    expect(formatContext(1_200_000)).toBe('1.2M');
  });

  it('switches to millions before the thousands round to a thousand k', () => {
    expect(formatContext(999_500)).toBe('1M');
    expect(formatContext(999_999)).toBe('1M');
    expect(formatContext(999_499)).toBe('999k');
  });

  it('shows nothing for a missing window', () => {
    expect(formatContext(0)).toBe('');
    expect(formatContext(undefined)).toBe('');
    expect(formatContext(-1)).toBe('');
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame, PermissionRequest } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { cinematic, setCinematic } from '../../state/cinematic';
import { closeDisplay, displayOpen } from '../../state/display';
import { effectsLevel, resetWebglProbe, sceneFailed } from '../../state/effects';
import { lowPower, motion, setMotion } from '../../state/motion';
import { openSessions, sessionsOpen } from '../../state/palette';
import { displayDefaults, initialState, reduce } from '../../state/reducer';
import { state } from '../../state/store';
import { App } from '../App';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const PERMISSION = (fx.permission_request.payload as unknown as { payload: PermissionRequest }).payload;
const root = document.createElement('div');
const original = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  // A context object is enough for the probe, so cinematic mode is allowed.
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live' };
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  closeDisplay();
  setCinematic(false);
  setMotion('full');
  lowPower.value = false;
  effectsLevel.value = 0;
  sceneFailed.value = false;
  sessionsOpen.value = false;
  state.value = initialState();
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
  vi.restoreAllMocks();
});

async function flush(turns = 6) {
  await act(async () => {
    for (let i = 0; i < turns; i++) await Promise.resolve();
  });
}

async function mountApp() {
  await act(() => { render(<App />, root); });
  await flush();
}

function hudDisplay() {
  return root.querySelector<HTMLButtonElement>('.hud__display');
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Display settings"]');
}

function button(text: string) {
  return [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((b) => (b.textContent ?? '').trim() === text) ?? null;
}

function input(name: string, value?: string) {
  return root.querySelector<HTMLInputElement>(`input[name="${name}"]${value === undefined ? '' : `[value="${value}"]`}`);
}

async function click(el: Element | null): Promise<void> {
  expect(el).not.toBeNull();
  await act(() => { (el as HTMLElement).click(); });
  await flush();
}

async function openFromHud() {
  const control = hudDisplay()!;
  control.focus();
  await click(control);
  expect(dialog()).not.toBeNull();
}

async function escape() {
  await act(() => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true }));
  });
  await flush();
}

describe('the display settings dialog', () => {
  it('writes the theme and applies it', async () => {
    const set = vi.spyOn(api, 'setConfig').mockResolvedValue({ ...displayDefaults, theme: 'solid' });
    await mountApp();
    await openFromHud();
    await click(input('display-theme', 'solid'));
    expect(set).toHaveBeenCalledWith('options.web_display.theme', 'solid');
    expect(state.value.display.theme).toBe('solid');
    expect(root.querySelector('.holodeck')!.getAttribute('data-theme')).toBe('solid');
  });

  it('shows a refused write inline and reverts the radio', async () => {
    vi.spyOn(api, 'setConfig').mockRejectedValue(new ApiError(500, 'config_write', 'could not write'));
    await mountApp();
    await openFromHud();
    await click(input('display-theme', 'solid'));
    expect(dialog()!.querySelector('[role="alert"]')?.textContent).toContain('could not write');
    expect(input('display-theme', 'holodeck')!.checked).toBe(true);
  });

  it('writes the low power checkbox and the motion choice', async () => {
    const set = vi.spyOn(api, 'setConfig').mockImplementation(async (key) => ({
      ...state.value.display,
      ...(key === 'options.web_display.low_power' ? { low_power: true } : {}),
      ...(key === 'options.web_display.motion' ? { motion: 'reduced' as const } : {}),
    }));
    await mountApp();
    await openFromHud();
    await click(input('display-low-power'));
    expect(set).toHaveBeenCalledWith('options.web_display.low_power', true);
    expect(lowPower.value).toBe(true);
    await click(input('display-motion', 'reduced'));
    expect(set).toHaveBeenCalledWith('options.web_display.motion', 'reduced');
    expect(motion.value).toBe('reduced');
  });

  it('removes the motion key for Follow the system', async () => {
    vi.spyOn(api, 'setConfig').mockResolvedValue({ ...displayDefaults, motion: 'reduced' });
    const remove = vi.spyOn(api, 'removeConfig').mockResolvedValue({ ...displayDefaults });
    await mountApp();
    await openFromHud();
    await click(input('display-motion', 'reduced'));
    await click(input('display-motion', ''));
    expect(remove).toHaveBeenCalledWith('options.web_display.motion');
    expect(input('display-motion', '')!.checked).toBe(true);
  });

  it('hides the orbit hint', async () => {
    vi.spyOn(api, 'setConfig').mockResolvedValue({ ...displayDefaults, cinematic_hint: false });
    await mountApp();
    await openFromHud();
    expect(input('display-hint')!.checked).toBe(true);
    await click(input('display-hint'));
    expect(api.setConfig).toHaveBeenCalledWith('options.web_display.cinematic_hint', false);
    await escape();
    expect(displayOpen.value).toBe(false);
    await act(() => { setCinematic(true); });
    expect(cinematic.value).toBe(true);
    expect(root.querySelector('.cine-hint')).toBeNull();
  });

  it('resets every setting', async () => {
    const remove = vi.spyOn(api, 'removeConfig').mockResolvedValue({ ...displayDefaults });
    await mountApp();
    await openFromHud();
    await click(button('Reset display settings'));
    expect(remove).toHaveBeenCalledTimes(4);
    expect(state.value.display).toEqual(displayDefaults);
  });

  it('offers restore effects while the effects are reduced', async () => {
    effectsLevel.value = 1;
    await mountApp();
    await openFromHud();
    const restore = button('Restore effects')!;
    await act(() => { restore.focus(); });
    await click(restore);
    expect(effectsLevel.value).toBe(0);
    expect(button('Restore effects')).toBeNull();
    expect(dialog()!.contains(document.activeElement)).toBe(true);
  });

  it('stops web mode after a confirm', async () => {
    const detach = vi.spyOn(api, 'detach').mockResolvedValue(undefined);
    await mountApp();
    await openFromHud();
    await click(button('Stop web mode'));
    expect(document.activeElement).toBe(button('Stop web mode now'));
    await click(button('Cancel'));
    expect(detach).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(button('Stop web mode'));
    await click(button('Stop web mode'));
    await click(button('Stop web mode now'));
    expect(detach).toHaveBeenCalledTimes(1);
    // The ended page behind the dialog carries the stopped sentence.
    expect(displayOpen.value).toBe(false);
    expect(dialog()).toBeNull();
  });

  it('moves focus in on open and returns it to the HUD control on close', async () => {
    await mountApp();
    const control = hudDisplay()!;
    await openFromHud();
    expect(dialog()!.contains(document.activeElement)).toBe(true);
    await escape();
    expect(displayOpen.value).toBe(false);
    expect(document.activeElement).toBe(control);
  });

  it('keeps its focus through a dialog that needs you', async () => {
    await mountApp();
    await openFromHud();
    const solid = input('display-theme', 'solid')!;
    solid.focus();
    expect(document.activeElement).toBe(solid);

    // A permission request makes the page behind the dialog inert.
    await act(() => { state.value = { ...state.value, permissions: [PERMISSION] }; });
    await flush();
    // jsdom has no inert, so the blur the browser performs is spelled out.
    (document.activeElement as HTMLElement | null)?.blur();
    expect(document.activeElement).toBe(document.body);

    // A re-render of the dialog while the request is open moves no focus.
    await act(() => { state.value = { ...state.value }; });
    await flush();
    expect(document.activeElement).toBe(document.body);

    // The link drops under the request, so the page is no longer inert, but
    // the request still needs the user and owns the focus.
    await act(() => { state.value = { ...state.value, conn: 'ended' }; });
    await flush();
    expect(document.activeElement).toBe(document.body);

    // The request clears: the dialog is the layer that stays open.
    await act(() => { state.value = { ...state.value, permissions: [] }; });
    await flush();
    expect(document.activeElement).toBe(solid);
  });

  it('closes another panel when it opens', async () => {
    await mountApp();
    openSessions();
    await act(() => { hudDisplay()!.click(); });
    await flush();
    expect(sessionsOpen.value).toBe(false);
    expect(displayOpen.value).toBe(true);
  });
});

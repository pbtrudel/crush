import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../state/__fixtures__/events.json';
import type { Frame } from '../api/types';
import { cinematic, setCinematic } from '../state/cinematic';
import { resetWebglProbe } from '../state/effects';
import { initialState, reduce } from '../state/reducer';
import { state } from '../state/store';
import { App } from './App';

// The Escape routing is the App's: the scene layer would otherwise fail the
// mode closed without a live scene, so the key handler is what this file
// exercises.
vi.mock('./stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const original = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  const s = reduce(initialState(), fx.snapshot);
  state.value = { ...s, conn: 'live', inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false } };
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
  vi.restoreAllMocks();
});

const escape = () => act(() => {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }));
});

describe('Escape in cinematic mode', () => {
  it('closes the inspector first and leaves the mode on the second press', async () => {
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    expect(cinematic.value).toBe(true);

    await escape();
    expect(state.value.inspected).toBeNull();
    expect(cinematic.value).toBe(true);
    expect(document.activeElement?.id).toBe('agent-sub-1');

    await escape();
    expect(cinematic.value).toBe(false);
  });

  it('closes only the display menu when Escape is pressed inside it', async () => {
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    const details = document.querySelector<HTMLDetailsElement>('details.motion-menu')!;
    details.open = true;
    const radio = details.querySelector<HTMLInputElement>('input[type="radio"]')!;
    radio.focus();

    await act(() => { radio.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true })); });

    expect(details.open).toBe(false);
    expect(state.value.inspected?.sessionId).toBe('sub-1');
    expect(cinematic.value).toBe(true);
    expect(document.activeElement).toBe(details.querySelector('summary'));
  });

  it('keeps a non-empty message draft, then closes once the box is empty', async () => {
    state.value = { ...state.value, inspected: { sessionId: 'team-m-1', session: null, messages: [], loading: false } };
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    const box = document.querySelector<HTMLTextAreaElement>('#inspector-message')!;
    await act(() => {
      box.value = 'hold that';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    box.focus();

    await act(() => { box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true })); });
    expect(state.value.inspected?.sessionId).toBe('team-m-1');
    expect(cinematic.value).toBe(true);
    expect(box.value).toBe('hold that');

    await act(() => {
      box.value = '';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => { box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true })); });
    expect(state.value.inspected).toBeNull();
    expect(cinematic.value).toBe(true);
  });
});

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { resetWebglProbe } from '../state/effects';
import { setCinematic } from '../state/cinematic';
import { initialState } from '../state/reducer';
import { state } from '../state/store';
import { Hud } from './Hud';

// The WebGL probe is cached and read outside the signal graph, so it has to be
// true before anything evaluates the cinematic rule for the first time.
const original = HTMLCanvasElement.prototype.getContext;
const root = document.createElement('div');

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  state.value = { ...initialState(), conn: 'live' };
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
});

describe('Hud in cinematic mode', () => {
  it('moves the identity block off screen but keeps the page title', async () => {
    setCinematic(true);
    await act(() => {
      render(<Hud />, root);
    });

    const id = root.querySelector('.hud__id')!;
    expect(id.classList.contains('visually-hidden')).toBe(true);
    expect(id.querySelector('h1.hud__title')!.textContent).toBe('New session');
    expect(root.querySelector('.hud__cinematic')!.getAttribute('aria-pressed')).toBe('true');
  });
});

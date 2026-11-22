import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState } from '../state/reducer';
import { state } from '../state/store';
import { App } from './App';

vi.mock('../scene/constellation', () => ({ mountConstellation: () => { throw new Error('no webgl'); } }));

describe('App layout', () => {
  it('puts reading surfaces on glass and glanceable ones on hologram, in the same DOM order', async () => {
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    await act(() => { render(<App />, root); });
    expect(root.querySelector('.hud')!.classList.contains('holo')).toBe(true);
    expect(root.querySelector('.panel--log')!.classList.contains('glass')).toBe(true);
    expect(root.querySelector('.holodeck__deck .deck')!.classList.contains('glass')).toBe(true);
    const order = Array.from(root.querySelectorAll('.hud, .panel--log, .stage, .holodeck__deck')).map((e) => e.className.split(' ')[0]);
    expect(order).toEqual(['hud', 'panel', 'stage', 'holodeck__deck']);
    render(null, root);
  });
});

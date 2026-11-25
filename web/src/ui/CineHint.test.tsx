import { describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { setCinematic } from '../state/cinematic';
import { resetWebglProbe, sceneFailed } from '../state/effects';
import { lowPower } from '../state/motion';
import { initialState } from '../state/reducer';
import { state } from '../state/store';
import { CineHint } from './CineHint';

describe('CineHint', () => {
  it('shows the orbit keys while in cinematic mode', async () => {
    const original = HTMLCanvasElement.prototype.getContext;
    // A context object is enough for the probe, so cinematic mode is allowed.
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    resetWebglProbe();
    lowPower.value = false;
    sceneFailed.value = false;
    const root = document.createElement('div');
    try {
      await act(() => { render(<CineHint />, root); });
      expect(root.querySelector('.cine-hint')).toBeNull();
      await act(() => { setCinematic(true); });
      expect(root.querySelector('.cine-hint')!.textContent).toContain('Shift + arrow keys');
      // The setting hides it without leaving cinematic mode.
      await act(() => {
        state.value = { ...state.value, display: { ...state.value.display, cinematic_hint: false } };
      });
      expect(root.querySelector('.cine-hint')).toBeNull();
      await act(() => { setCinematic(false); });
      state.value = initialState();
      expect(root.querySelector('.cine-hint')).toBeNull();
    } finally {
      await act(() => { render(null, root); });
      HTMLCanvasElement.prototype.getContext = original;
      resetWebglProbe();
      state.value = initialState();
    }
  });
});

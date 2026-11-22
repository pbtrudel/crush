import { describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { Stage } from './Stage';
import { lowPower } from '../../state/motion';
import { resetWebglProbe, sceneFailed, use3D } from '../../state/effects';

describe('Stage', () => {
  it('renders the 2D constellation and an accessible agent list without WebGL', () => {
    resetWebglProbe();
    sceneFailed.value = false;
    lowPower.value = false;
    const root = document.createElement('div');
    render(<Stage />, root);
    expect(root.querySelector('svg.constellation-svg')).not.toBeNull();
    const agent = root.querySelector<HTMLElement>('[aria-label="Agents"] li');
    expect(agent?.textContent).toContain('Coder');
    // M1 has one agent node; it must be keyboard focusable. Roving tabindex
    // across several agents lands with the M2 inspector.
    expect(agent?.tabIndex).toBe(0);
  });

  it('shows no 2D constellation while 3D runs, and brings it back when the scene fails', async () => {
    const original = HTMLCanvasElement.prototype.getContext;
    // A context object is enough for the probe; the loss extension is absent.
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    resetWebglProbe();
    lowPower.value = false;
    sceneFailed.value = false;
    const root = document.createElement('div');
    try {
      await act(() => {
        render(<Stage />, root);
      });
      expect(use3D()).toBe(true);
      expect(root.querySelector('svg.constellation-svg')).toBeNull();
      // The agent list stays in either mode.
      expect(root.querySelector('[aria-label="Agents"] li')).not.toBeNull();

      await act(() => {
        sceneFailed.value = true;
      });
      expect(root.querySelector('svg.constellation-svg')).not.toBeNull();
    } finally {
      render(null, root);
      HTMLCanvasElement.prototype.getContext = original;
      sceneFailed.value = false;
      resetWebglProbe();
    }
  });
});

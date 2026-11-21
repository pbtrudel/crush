import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { Stage } from './Stage';
import { lowPower } from '../../state/motion';

vi.mock('../../scene/constellation', () => ({
  mountConstellation: () => {
    throw new Error('no webgl');
  },
}));

describe('Stage', () => {
  it('renders the 2D constellation and an accessible agent list without WebGL', () => {
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

  it('falls back to the 2D constellation when the 3D scene fails to start', async () => {
    const original = HTMLCanvasElement.prototype.getContext;
    // A context object is enough for the probe; the loss extension is absent.
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    lowPower.value = false;
    const root = document.createElement('div');
    try {
      render(<Stage />, root);
      await vi.waitFor(() => expect(root.querySelector('svg.constellation-svg')).not.toBeNull());
    } finally {
      render(null, root);
      HTMLCanvasElement.prototype.getContext = original;
    }
  });
});

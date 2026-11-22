import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { Stage } from './Stage';
import { lowPower } from '../../state/motion';
import { resetWebglProbe, sceneFailed, use3D } from '../../state/effects';
import { stageRect } from '../../state/layout';
import { initialState, reduce } from '../../state/reducer';
import { actions, state } from '../../state/store';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame } from '../../api/types';

const fx = fixtures as unknown as Record<string, Frame>;

describe('Stage', () => {
  it('renders the 2D constellation and an accessible agent list without WebGL', () => {
    resetWebglProbe();
    sceneFailed.value = false;
    lowPower.value = false;
    const root = document.createElement('div');
    render(<Stage />, root);
    expect(root.querySelector('svg.constellation-svg')).not.toBeNull();
    const agent = root.querySelector<HTMLElement>('[role="listbox"][aria-label="Agents"] [role="option"]');
    expect(agent?.textContent).toContain('Coder');
    // The list is one tab stop; the arrows move the roving focus inside it.
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

  it('publishes the stage box for the projector', async () => {
    const root = document.createElement('div');
    await act(() => { render(<Stage />, root); });
    expect(stageRect.value).not.toBeNull();
    await act(() => { render(null, root); });
    expect(stageRect.value).toBeNull();
  });

  it('draws one 2D node per agent and selects it on click', async () => {
    resetWebglProbe();
    lowPower.value = true;
    state.value = reduce(initialState(), fx.snapshot);
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    const root = document.createElement('div');
    try {
      await act(() => { render(<Stage />, root); });
      const nodes = root.querySelectorAll('[data-agent-node]');
      expect(nodes).toHaveLength(2);
      await act(() => { (root.querySelector('[data-agent-node="team-m-1"]') as SVGGElement).dispatchEvent(new MouseEvent('click', { bubbles: true })); });
      expect(inspect).toHaveBeenCalledWith('team-m-1');
    } finally {
      render(null, root);
      lowPower.value = false;
      vi.restoreAllMocks();
    }
  });
});

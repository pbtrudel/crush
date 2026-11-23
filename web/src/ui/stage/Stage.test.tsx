import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { Stage } from './Stage';
import { ConstellationSvg } from './ConstellationSvg';
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

  it('never shows 2D nodes and 3D tags at once, so an agent id appears once', async () => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    resetWebglProbe();
    lowPower.value = false;
    sceneFailed.value = false;
    state.value = reduce(initialState(), fx.snapshot);
    const root = document.createElement('div');
    const ids = () => [...root.querySelectorAll('[data-agent-node]')].map((n) => n.getAttribute('data-agent-node'));
    try {
      await act(() => { render(<Stage />, root); });
      expect(use3D()).toBe(true);
      expect(root.querySelector('svg.constellation-svg')).toBeNull();
      expect(root.querySelector('.agent-tag')).not.toBeNull();
      expect(ids().length).toBeGreaterThan(0);
      expect(new Set(ids()).size).toBe(ids().length);

      // The 2D fallback replaces the tags rather than joining them.
      await act(() => { sceneFailed.value = true; });
      expect(root.querySelector('svg.constellation-svg')).not.toBeNull();
      expect(root.querySelector('.agent-tag')).toBeNull();
      expect(ids().length).toBeGreaterThan(0);
      expect(new Set(ids()).size).toBe(ids().length);
    } finally {
      render(null, root);
      state.value = initialState();
      HTMLCanvasElement.prototype.getContext = original;
      sceneFailed.value = false;
      resetWebglProbe();
    }
  });

  it('keeps the ring and every node inside the 2D viewBox', () => {
    for (const count of [1, 2, 3, 5]) {
      const root = document.createElement('div');
      const agents = Array.from({ length: count }, (_, i) => ({
        id: `a-${i}`, kind: 'teammate' as const, status: 'running' as const, name: `agent ${i}`,
      }));
      render(
        <ConstellationSvg fill={0} busy={false} attention={false} agents={agents} selected={null} onSelect={() => {}} />,
        root,
      );
      const svg = root.querySelector('svg')!;
      const [, minY, , height] = svg.getAttribute('viewBox')!.split(/\s+/).map(Number);
      const bottom = minY + height;
      // The outer ring bottoms out at cy plus ry in the same units.
      const ring = svg.querySelector('.cs-ring')!;
      expect(Number(ring.getAttribute('cy')) + Number(ring.getAttribute('ry'))).toBeLessThanOrEqual(bottom);
      for (const node of svg.querySelectorAll('[data-agent-node]')) {
        const y = Number(/translate\(\s*\S+\s+(\S+)\s*\)/.exec(node.getAttribute('transform')!)![1]);
        const hex = [...node.querySelector('.cs-node__hex')!.getAttribute('points')!.matchAll(/([-\d.]+),([-\d.]+)/g)]
          .reduce((low, [, , py]) => Math.max(low, Math.abs(Number(py))), 0);
        expect(y + hex).toBeLessThanOrEqual(bottom);
      }
      render(null, root);
    }
  });
});

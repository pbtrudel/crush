import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { SceneLayer } from './SceneLayer';
import { lowPower } from '../../state/motion';
import { resetWebglProbe, sceneFailed, use3D } from '../../state/effects';

vi.mock('../../scene/constellation', () => ({
  mountConstellation: () => {
    throw new Error('no webgl');
  },
}));

const root = document.createElement('div');
let original: typeof HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  original = HTMLCanvasElement.prototype.getContext;
  // A context object is enough for the probe; the loss extension is absent.
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  lowPower.value = false;
  sceneFailed.value = false;
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  HTMLCanvasElement.prototype.getContext = original;
  sceneFailed.value = false;
  resetWebglProbe();
});

describe('SceneLayer', () => {
  it('mounts the full-viewport canvas and falls back when the scene throws', async () => {
    expect(use3D()).toBe(true);
    await act(() => {
      render(<SceneLayer />, root);
    });
    const canvas = root.querySelector('canvas.scene-layer__canvas');
    expect(canvas).not.toBeNull();
    expect(canvas?.getAttribute('aria-hidden')).toBe('true');

    // The throw lands in the mount promise's catch, which marks the scene
    // failed and hands the stage back to the 2D constellation.
    await vi.waitFor(() => {
      expect(sceneFailed.value).toBe(true);
    });
    await act(() => {});
    expect(root.querySelector('canvas.scene-layer__canvas')).toBeNull();
    expect(use3D()).toBe(false);
  });
});

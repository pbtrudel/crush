import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { lowPower } from '../../state/motion';
import { resetWebglProbe, sceneFailed, use3D } from '../../state/effects';
import { emptyTelemetry, telemetry, type ToolEvent } from '../../state/telemetry';

const mount = vi.hoisted(() => ({
  calls: 0,
  throwOnMount: false,
  burst: vi.fn(),
  onFail: undefined as undefined | (() => void),
}));
vi.mock('../../scene/mount', () => ({
  mountScene: (_c: HTMLCanvasElement, _s: unknown, h: { onFail(): void }) => {
    mount.calls++;
    if (mount.throwOnMount) throw new Error('no webgl');
    mount.onFail = h.onFail;
    return {
      update() {}, burst: mount.burst, dispose() {}, requestFrame() {},
      setCameraPose() {}, setCameraDriver() {},
    };
  },
}));

const ev = (id: string, category: ToolEvent['category']): ToolEvent => ({ id, category, at: 1 });
const original = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  // A context object is enough for the probe; the loss extension is absent.
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  lowPower.value = false;
  sceneFailed.value = false;
  telemetry.value = emptyTelemetry();
  mount.calls = 0;
  mount.throwOnMount = false;
  mount.burst.mockClear();
});

afterEach(() => {
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
  sceneFailed.value = false;
});

async function mountLayer() {
  const { SceneLayer } = await import('./SceneLayer');
  const root = document.createElement('div');
  await act(() => {
    render(<SceneLayer />, root);
  });
  await vi.waitFor(() => expect(mount.calls).toBe(1));
  return root;
}

describe('SceneLayer', () => {
  it('marks the scene failed when mount reports failure', async () => {
    const root = await mountLayer();
    expect(sceneFailed.value).toBe(false);
    mount.onFail!();
    expect(sceneFailed.value).toBe(true);
    render(null, root);
  });

  it('marks the scene failed when mount throws, and hands the stage back', async () => {
    mount.throwOnMount = true;
    const { SceneLayer } = await import('./SceneLayer');
    const root = document.createElement('div');
    await act(() => {
      render(<SceneLayer />, root);
    });
    const canvas = root.querySelector('canvas.scene-layer__canvas');
    expect(canvas).not.toBeNull();
    expect(canvas?.getAttribute('aria-hidden')).toBe('true');
    await vi.waitFor(() => expect(sceneFailed.value).toBe(true));
    await act(() => {});
    expect(root.querySelector('canvas.scene-layer__canvas')).toBeNull();
    expect(use3D()).toBe(false);
    render(null, root);
  });

  it('does not burst for tool events that were already there when it mounted', async () => {
    telemetry.value = { ...emptyTelemetry(), tools: [ev('old1', 'read'), ev('old2', 'run')] };
    const root = await mountLayer();
    expect(mount.burst).not.toHaveBeenCalled();

    await act(() => {
      telemetry.value = { ...telemetry.value, tools: [...telemetry.value.tools, ev('new1', 'write')] };
    });
    expect(mount.burst).toHaveBeenCalledTimes(1);
    expect(mount.burst).toHaveBeenCalledWith('write');
    render(null, root);
  });

  it('bursts every event when older ones were pruned from the window', async () => {
    telemetry.value = { ...emptyTelemetry(), tools: [ev('old1', 'read')] };
    const root = await mountLayer();
    await act(() => {
      telemetry.value = { ...telemetry.value, tools: [ev('new1', 'run'), ev('new2', 'network')] };
    });
    expect(mount.burst.mock.calls.map((c) => c[0])).toEqual(['run', 'network']);
    render(null, root);
  });
});

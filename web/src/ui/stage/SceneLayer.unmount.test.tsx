import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { resetWebglProbe, sceneFailed } from '../../state/effects';
import { lowPower } from '../../state/motion';
import { initialState } from '../../state/reducer';
import { state } from '../../state/store';
import { emptyTelemetry, telemetry } from '../../state/telemetry';

// Hold the scene chunk until the test opens the gate, the way a slow network
// does. Both mounts below await the same promise, so their callbacks run in
// the order the effects started them.
const gate = vi.hoisted(() => {
  let release = () => {};
  const ready = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { ready, release: () => release() };
});

const mount = vi.hoisted(() => ({ calls: 0, disposed: 0 }));

vi.mock('../../scene/mount', async () => {
  await gate.ready;
  return {
    mountScene: () => {
      mount.calls++;
      return {
        update() {},
        burst() {},
        dispose() {
          mount.disposed++;
        },
        requestFrame() {},
        setCameraPose() {},
        setCameraDriver() {},
      };
    },
  };
});

const original = HTMLCanvasElement.prototype.getContext;

// Every root is unmounted inside act: a root left mounted past the test
// queues preact's after-paint flush on a 35ms timeout that can fire after
// jsdom has torn down, where cancelAnimationFrame no longer exists.
const roots: HTMLElement[] = [];

function newRoot(): HTMLElement {
  const root = document.createElement('div');
  roots.push(root);
  return root;
}

async function unmount(root: HTMLElement) {
  const i = roots.indexOf(root);
  if (i >= 0) roots.splice(i, 1);
  await act(() => {
    render(null, root);
  });
}

beforeEach(() => {
  // A context object is enough for the probe; the loss extension is absent.
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  lowPower.value = false;
  sceneFailed.value = false;
  state.value = initialState();
  telemetry.value = emptyTelemetry();
  mount.calls = 0;
  mount.disposed = 0;
});

afterEach(async () => {
  gate.release();
  for (const root of roots.splice(0)) {
    await act(() => {
      render(null, root);
    });
  }
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
  lowPower.value = false;
  sceneFailed.value = false;
});

describe('SceneLayer unmounting while the scene chunk loads', () => {
  it('drops the stale mount when the layer remounts before the chunk settles', async () => {
    const { SceneLayer } = await import('./SceneLayer');
    const root = newRoot();
    await act(() => {
      render(<SceneLayer />, root);
    });

    // Low power tears the layer down to the 2D view, and turning it off
    // mounts the scene again. By now the canvas ref belongs to the second
    // mount while the first effect's import is still in flight.
    await act(() => {
      lowPower.value = true;
    });
    await act(() => {
      lowPower.value = false;
    });

    gate.release();
    // A macrotask runs after every promise callback the release queued.
    await new Promise((resolve) => setTimeout(resolve, 0));
    await act(() => {});

    // The stale mount is dropped; only the live layer mounts a scene.
    expect(mount.calls).toBe(1);
    await unmount(root);
    expect(mount.disposed).toBe(1);
  });
});

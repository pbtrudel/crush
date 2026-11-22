import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { BOOT_MS, bootFrame } from './boot';
import { mountScene, resetBootLatch, type SceneHandle, type SceneInput } from './mount';

type Spy = ReturnType<typeof vi.fn>;

interface StubRenderer {
  disposed: number;
  lost: number;
  renders: number;
}

interface StubPart {
  dispose: Spy;
}

interface StubWorld extends StubPart {
  setReveal: Spy;
}

// The stubs record what the mount does to them. They are hoisted above the
// module mocks below, which read and fill them.
const renderers = vi.hoisted(() => [] as StubRenderer[]);
const composers = vi.hoisted(() => [] as Array<{ disposed: number; renders: number }>);
const parts = vi.hoisted(() => ({
  worlds: [] as StubWorld[],
  cores: [] as StubPart[],
  bursts: [] as StubPart[],
}));
const failing = vi.hoisted(() => ({ composer: false }));

vi.mock('three', async (importOriginal) => {
  const actual = await importOriginal<typeof import('three')>();
  class StubRenderer {
    domElement: HTMLCanvasElement;
    toneMapping = 0;
    disposed = 0;
    lost = 0;
    renders = 0;
    constructor(params: { canvas: HTMLCanvasElement }) {
      this.domElement = params.canvas;
      renderers.push(this);
    }
    setPixelRatio() {}
    setSize() {}
    render() {
      this.renders++;
    }
    dispose() {
      this.disposed++;
    }
    forceContextLoss() {
      this.lost++;
    }
  }
  return { ...actual, WebGLRenderer: StubRenderer };
});

vi.mock('three/addons/postprocessing/EffectComposer.js', () => ({
  EffectComposer: class {
    disposed = 0;
    renders = 0;
    passes: unknown[] = [];
    constructor() {
      if (failing.composer) throw new Error('composer failed');
      composers.push(this);
    }
    addPass(pass: unknown) {
      this.passes.push(pass);
    }
    setSize() {}
    render() {
      this.renders++;
    }
    dispose() {
      this.disposed++;
    }
  },
}));

vi.mock('three/addons/postprocessing/RenderPass.js', () => ({
  RenderPass: class {},
}));

vi.mock('three/addons/postprocessing/OutputPass.js', () => ({
  OutputPass: class {
    disposed = 0;
    dispose() {
      this.disposed++;
    }
  },
}));

vi.mock('three/addons/postprocessing/UnrealBloomPass.js', () => ({
  UnrealBloomPass: class {
    disposed = 0;
    setSize() {}
    dispose() {
      this.disposed++;
    }
  },
}));

// The world, core and bursts build real GPU resources. The mount only wires
// them, so stand-ins keep these tests about the mount and make the dispose
// assertions exact.
vi.mock('./world', async (importOriginal) => {
  const { Group } = await import('three');
  return {
    ...(await importOriginal<typeof import('./world')>()),
    createWorld: () => {
      const world = {
        group: new Group(),
        particles: new Group(),
        beams: new Group(),
        floor: new Group(),
        setEffects: vi.fn(),
        setReveal: vi.fn(),
        update: vi.fn(),
        mirror: vi.fn(),
        dispose: vi.fn(),
      };
      parts.worlds.push(world);
      return world;
    },
  };
});

vi.mock('./core', async (importOriginal) => {
  const { Group } = await import('three');
  return {
    ...(await importOriginal<typeof import('./core')>()),
    createCore: () => {
      const core = {
        group: new Group(),
        emissive: new Group(),
        setMode: vi.fn(),
        setFill: vi.fn(),
        flash: vi.fn(),
        setBoot: vi.fn(),
        update: vi.fn(() => false),
        dispose: vi.fn(),
      };
      parts.cores.push(core);
      return core;
    },
  };
});

vi.mock('./bursts', async (importOriginal) => {
  const { Group } = await import('three');
  return {
    ...(await importOriginal<typeof import('./bursts')>()),
    createBursts: () => {
      const bursts = {
        group: new Group(),
        spawn: vi.fn(),
        update: vi.fn(() => false),
        active: vi.fn(() => 0),
        dispose: vi.fn(),
      };
      parts.bursts.push(bursts);
      return bursts;
    },
  };
});

class StubResizeObserver {
  static instances: StubResizeObserver[] = [];
  disconnected = false;
  observed: Element[] = [];
  constructor(readonly callback: ResizeObserverCallback) {
    StubResizeObserver.instances.push(this);
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

let frames: FrameRequestCallback[] = [];
let clock = 0;
const mounted: SceneHandle[] = [];

function advance(ms: number) {
  clock += ms;
  const due = frames;
  frames = [];
  for (const callback of due) callback(clock);
}

function runFrames(count: number, step = 1000 / 60) {
  for (let i = 0; i < count; i++) advance(step);
}

function canvas(): HTMLCanvasElement {
  const el = document.createElement('canvas');
  Object.defineProperty(el, 'clientWidth', { value: 800 });
  Object.defineProperty(el, 'clientHeight', { value: 600 });
  document.body.append(el);
  return el;
}

const hooks = () => ({ onStepDown: vi.fn(), onFail: vi.fn() });

function start(over: Partial<SceneInput> = {}): SceneHandle {
  const scene = mountScene(canvas(), input(over), hooks());
  mounted.push(scene);
  return scene;
}

function input(over: Partial<SceneInput> = {}): SceneInput {
  return {
    mode: 'idle',
    fill: 0.5,
    motion: 'full',
    effects: { bloom: true, beams: true, particleShare: 1, cinematic: true },
    booting: false,
    focus: null,
    ...over,
  };
}

// The mount keeps module state (the boot latch) and every test wants the
// power-up already done, so each one starts with a throwaway mount in off
// motion, which marks the page booted without animating.
beforeEach(() => {
  frames = [];
  clock = 0;
  renderers.length = 0;
  composers.length = 0;
  parts.worlds.length = 0;
  parts.cores.length = 0;
  parts.bursts.length = 0;
  failing.composer = false;
  vi.spyOn(performance, 'now').mockImplementation(() => clock);
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.push(callback);
    return frames.length;
  });
  vi.stubGlobal('cancelAnimationFrame', () => {
    frames = [];
  });
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  resetBootLatch();
  const warm = mountScene(canvas(), input({ motion: 'off' }), hooks());
  warm.dispose();
  frames = [];
  renderers.length = 0;
  composers.length = 0;
  parts.worlds.length = 0;
  parts.cores.length = 0;
  parts.bursts.length = 0;
});

afterEach(() => {
  // Listeners live on the shared jsdom window, so a scene left mounted here
  // would answer the next test's events.
  for (const scene of mounted) scene.dispose();
  mounted.length = 0;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe('mountScene', () => {
  it('runs driver frames in off motion until the driver reports idle, then stops', () => {
    const scene = start({ motion: 'off' });
    advance(1);

    let call = 0;
    const driver = vi.fn(() => {
      call++;
      return call < 3;
    });
    scene.setCameraDriver(driver);

    advance(16);
    advance(16);
    advance(16);
    expect(driver).toHaveBeenCalledTimes(3);
    // No ambient frames are left, so an off-motion scene goes quiet.
    expect(frames).toHaveLength(0);
    expect(composers.at(-1)!.renders).toBe(4);
    advance(16);
    expect(driver).toHaveBeenCalledTimes(3);
  });

  it('runs an idle driver in full motion at the ambient rate, not 60 fps', () => {
    const scene = start({ motion: 'full' });
    advance(1);

    const driver = vi.fn(() => false);
    scene.setCameraDriver(driver);
    // Entering cinematic mode also eases the focus offset away, which is an
    // event; let that finish before measuring the idle scene.
    runFrames(90);
    driver.mockClear();
    runFrames(10);
    const calls = driver.mock.calls.length;
    expect(calls).toBeGreaterThanOrEqual(3);
    expect(calls).toBeLessThanOrEqual(6);
  });

  it('snaps a camera pose when asked instead of easing it', () => {
    const scene = start({ motion: 'off' });
    const pose = { position: new THREE.Vector3(0, 4.6, 11.5), target: new THREE.Vector3(0, 1.2, 0) };

    scene.setCameraPose(pose);
    advance(16);
    expect(scene.camera.position.distanceTo(pose.position)).toBeGreaterThan(0.5);

    scene.setCameraPose(pose, { snap: true });
    expect(scene.camera.position.equals(pose.position)).toBe(true);
  });

  it('eases the focus offset away over the flight and keeps the camera heading', () => {
    const focus = { x: 0, y: 0, width: 700, height: 800 };
    const scene = start({ motion: 'full', focus });
    advance(1);
    // The stage offset is 50 px here, the gap between the canvas center and
    // the focus box center.
    expect(scene.camera.view?.enabled).toBe(true);
    expect(scene.camera.view?.offsetX).toBeCloseTo(50);

    scene.setCameraDriver(() => false);
    advance(17);
    expect(scene.camera.view?.offsetX).toBeGreaterThan(0);
    expect(scene.camera.view?.offsetX).toBeLessThan(50);
    runFrames(70);
    expect(scene.camera.view?.enabled).toBe(false);
  });

  it('snaps the focus offset away outside full motion', () => {
    const focus = { x: 0, y: 0, width: 700, height: 800 };
    const scene = start({ motion: 'off', focus });
    advance(1);
    expect(scene.camera.view?.enabled).toBe(true);

    scene.setCameraDriver(() => false);
    advance(17);
    expect(scene.camera.view?.enabled).toBe(false);
  });

  it('keeps the camera heading when the driver hands it back', () => {
    const scene = start({ motion: 'off' });
    advance(1);
    scene.setCameraDriver((cam) => {
      cam.position.set(6, 4, 6);
      cam.lookAt(2, 3, 0);
      return false;
    });
    advance(16);
    const before = scene.camera.getWorldDirection(new THREE.Vector3());

    scene.setCameraDriver(null);
    advance(16);
    const after = scene.camera.getWorldDirection(new THREE.Vector3());
    expect(after.dot(before)).toBeGreaterThan(0.9999);
  });

  it('does not kick a frame when the input did not change', () => {
    const scene = start({ motion: 'off' });
    advance(1);
    expect(frames).toHaveLength(0);

    scene.update(input({ motion: 'off' }));
    expect(frames).toHaveLength(0);

    scene.update(input({ fill: 0.9 }));
    expect(frames).toHaveLength(1);
  });

  it('disposes everything it built and stops every frame source', () => {
    const scene = start({ motion: 'full' });
    const renderer = renderers.at(-1)!;
    const composer = composers.at(-1)!;
    const world = parts.worlds.at(-1)!;
    const core = parts.cores.at(-1)!;
    const bursts = parts.bursts.at(-1)!;
    const observer = StubResizeObserver.instances.at(-1)!;
    expect(frames.length).toBeGreaterThan(0);

    scene.dispose();
    expect(frames).toHaveLength(0);
    expect(observer.disconnected).toBe(true);
    expect(world.dispose).toHaveBeenCalledTimes(1);
    expect(core.dispose).toHaveBeenCalledTimes(1);
    expect(bursts.dispose).toHaveBeenCalledTimes(1);
    expect(composer.disposed).toBe(1);
    expect(renderer.disposed).toBe(1);
    expect(renderer.lost).toBe(1);

    // The window and document listeners are gone: neither wakes the loop.
    window.dispatchEvent(new Event('pointermove'));
    document.dispatchEvent(new Event('visibilitychange'));
    expect(frames).toHaveLength(0);
  });

  it('disposes the renderer and frees its context when the mount throws', () => {
    failing.composer = true;
    expect(() => mountScene(canvas(), input(), hooks())).toThrow('composer failed');
    const renderer = renderers.at(-1)!;
    expect(renderer.disposed).toBe(1);
    expect(renderer.lost).toBe(1);
  });

  it('runs the power-up on the first mount and opens at the end state after it', () => {
    resetBootLatch();
    const first = start();
    const firstWorld = parts.worlds.at(-1)!;
    expect(firstWorld.setReveal).toHaveBeenLastCalledWith(0);
    runFrames(90);
    expect(firstWorld.setReveal.mock.calls.at(-1)![0]).toBeGreaterThan(0);
    first.dispose();

    start();
    const secondWorld = parts.worlds.at(-1)!;
    expect(secondWorld.setReveal).toHaveBeenCalledTimes(1);
    expect(secondWorld.setReveal).toHaveBeenCalledWith(bootFrame(BOOT_MS, 'full').reveal);
  });
});

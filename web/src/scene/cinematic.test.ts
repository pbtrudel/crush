import { afterEach, describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { HOME, WIDE } from './camera';
import { anchorsFor, startCinematic } from './cinematic';
import type { SceneHandle } from './mount';

interface FakeControls {
  target: THREE.Vector3;
  listeners: Map<string, Set<() => void>>;
  disposed: boolean;
  emit(name: string): void;
}

// The recording stub the OrbitControls mock pushes into. It is hoisted above
// the module mock below, which fills it.
const controls = vi.hoisted(() => ({ instances: [] as FakeControls[] }));

vi.mock('three/addons/controls/OrbitControls.js', async () => {
  const { Vector3 } = await import('three');
  class StubControls {
    target = new Vector3();
    listeners = new Map<string, Set<() => void>>();
    disposed = false;
    constructor() {
      controls.instances.push(this);
    }
    addEventListener(name: string, fn: () => void) {
      const set = this.listeners.get(name) ?? new Set();
      set.add(fn);
      this.listeners.set(name, set);
    }
    removeEventListener(name: string, fn: () => void) {
      this.listeners.get(name)?.delete(fn);
    }
    listenToKeyEvents() {}
    update() {}
    dispose() {
      this.disposed = true;
    }
    emit(name: string) {
      for (const fn of this.listeners.get(name) ?? []) fn();
    }
  }
  return { OrbitControls: StubControls };
});

function div(): HTMLDivElement {
  const el = document.createElement('div');
  document.body.append(el);
  return el;
}

// The handle and its spies, kept apart so the test can read the calls the
// scene makes through the interface.
function scene() {
  const camera = new THREE.PerspectiveCamera(42, 1, 0.1, 100);
  camera.position.set(0, 4.6, 11.5);
  camera.lookAt(0, 1.2, 0);
  camera.updateMatrixWorld();
  const requestFrame = vi.fn();
  const setCameraDriver = vi.fn();
  const setCameraPose = vi.fn();
  const handle: SceneHandle = {
    camera,
    canvas: document.createElement('canvas'),
    world: new THREE.Scene(),
    requestFrame,
    setCameraPose,
    setCameraDriver,
    update: vi.fn(),
    burst: vi.fn(),
    dispose: vi.fn(),
  };
  return { handle, requestFrame, setCameraDriver };
}

afterEach(() => {
  controls.instances.length = 0;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe('startCinematic', () => {
  it('repaints for a controls change and gives the canvas back on stop', async () => {
    // The flight's own frames come from the mount; here only stop()'s wait
    // for home runs, and a clock that jumps past its deadline ends it.
    let clock = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => (clock += 1000));
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });
    const { handle: s, requestFrame, setCameraDriver } = scene();
    const els = { view: div(), camera: div(), panels: [div()] };

    const started = startCinematic(s, els, 'full');
    const fake = controls.instances.at(-1)!;

    // Dragging or zooming changes the camera without a state change, so the
    // scene needs a frame for each change.
    expect(fake.listeners.get('change')?.size).toBe(1);
    expect(requestFrame).not.toHaveBeenCalled();
    fake.emit('change');
    expect(requestFrame).toHaveBeenCalledTimes(1);

    // The canvas takes the keyboard focus ring and drops aria-hidden while
    // it is focusable.
    expect(s.canvas.getAttribute('tabindex')).toBe('0');
    expect(s.canvas.getAttribute('aria-label')).toBe('3D view, shift plus arrow keys orbit');
    expect(s.canvas.hasAttribute('aria-hidden')).toBe(false);

    // A driver frame projects the log from this frame's camera, including a
    // focus offset the mount is easing away.
    s.camera.setViewOffset(1440, 860, 255, 0, 1440, 860);
    const driver = setCameraDriver.mock.calls.at(-1)![0] as (cam: THREE.PerspectiveCamera, dt: number) => boolean;
    driver(s.camera, 1 / 60);
    expect(els.view.style.transform).toContain('translate');
    expect(els.camera.style.transform).toContain('perspective(');
    expect(els.panels[0].style.transform).toContain('matrix3d(');

    await started.stop();

    expect(fake.disposed).toBe(true);
    expect(fake.listeners.get('change')?.size ?? 0).toBe(0);
    expect(setCameraDriver).toHaveBeenLastCalledWith(null);
    expect(s.canvas.getAttribute('aria-hidden')).toBe('true');
    expect(s.canvas.hasAttribute('tabindex')).toBe(false);
    expect(s.canvas.hasAttribute('aria-label')).toBe(false);
    // The wrappers and the panel are projected only while the mode is on.
    expect(els.view.style.transform).toBe('');
    expect(els.camera.style.transform).toBe('');
    expect(els.panels[0].style.transform).toBe('');
  });

  it('turns the inspector screen toward the camera and leaves the log alone', async () => {
    let clock = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => (clock += 1000));
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });
    // Off motion skips the flight, so the camera stays wherever the test
    // puts it and only the anchors react to the move.
    const { handle: s, setCameraDriver } = scene();
    const els = { view: div(), camera: div(), panels: [div(), div()] };

    const started = startCinematic(s, els, 'off');
    const driver = setCameraDriver.mock.calls.at(-1)![0] as (cam: THREE.PerspectiveCamera, dt: number) => boolean;
    s.camera.position.set(0, 4.6, 11.5);
    driver(s.camera, 1 / 60);
    const log = els.panels[0].style.transform;
    const inspector = els.panels[1].style.transform;

    // Orbiting around the projector moves the camera, not the screens: only
    // the inspector follows it.
    s.camera.position.set(-6, 4.6, 8);
    driver(s.camera, 1 / 60);
    expect(els.panels[0].style.transform).toBe(log);
    expect(els.panels[1].style.transform).not.toBe(inspector);

    await started.stop();
  });
});

describe('anchorsFor', () => {
  it('gives the log and the inspector their own screens', () => {
    const [log, inspector] = anchorsFor([document.createElement('section'), document.createElement('section')]);
    expect(log.position.x).toBeLessThan(0);
    expect(inspector.position.x).toBeGreaterThan(0);
  });

  it('places each panel at its own anchor inside the wide camera frustum', () => {
    const panels = [document.createElement('section'), document.createElement('section')];
    const [log, inspector] = anchorsFor(panels);

    // The two screens sit apart, turn opposite ways and only the inspector
    // faces the camera.
    expect(log.position.distanceTo(inspector.position)).toBeGreaterThan(1);
    expect(log.rotation.y).toBeGreaterThan(0);
    expect(inspector.rotation.y).toBeLessThan(0);
    expect(log.userData.faceCamera).toBe(false);
    expect(inspector.userData.faceCamera).toBe(true);

    // Both are in view from the cinematic pull-back, which is the pose the
    // flight looks through.
    const camera = new THREE.PerspectiveCamera(HOME.fov, 16 / 10, 0.1, 100);
    camera.position.copy(WIDE.position);
    camera.lookAt(WIDE.target);
    camera.updateMatrixWorld();
    const frustum = new THREE.Frustum().setFromProjectionMatrix(
      new THREE.Matrix4().multiplyMatrices(camera.projectionMatrix, camera.matrixWorldInverse),
    );
    expect(frustum.containsPoint(log.position)).toBe(true);
    expect(frustum.containsPoint(inspector.position)).toBe(true);

  });
});

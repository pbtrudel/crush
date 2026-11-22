import * as THREE from 'three';
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js';
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js';
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js';
import { UnrealBloomPass } from 'three/addons/postprocessing/UnrealBloomPass.js';
import { effectsLevel, type EffectFlags, type EffectsLevel } from '../state/effects';
import type { CoreMode } from '../state/levels';
import type { Motion } from '../state/motion';
import type { ToolCategory } from '../state/toolCategory';
import { BOOT_IGNITE_MS, BOOT_MS, bootFrame } from './boot';
import { categoryColor, createBursts } from './bursts';
import { HOME, parallax, viewOffset } from './camera';
import { readColors } from './colors';
import { watchContextLoss } from './contextLoss';
import { createCore } from './core';
import { FrameGuard } from './guard';
import { clampStep, frameInterval } from './loop';
import { motionScale } from './motionScale';
import { sceneSeed } from './rng';
import { createWorld } from './world';

export interface SceneInput {
  mode: CoreMode;
  fill: number;
  motion: Motion;
  effects: EffectFlags;
  booting: boolean;
  focus: { x: number; y: number; width: number; height: number } | null;
}

export type CameraDriver = (camera: THREE.PerspectiveCamera, dt: number) => void;

export interface SceneHandle {
  update(s: SceneInput): void;
  burst(c: ToolCategory): void;
  dispose(): void;
  camera: THREE.PerspectiveCamera;
  world: THREE.Scene;
  requestFrame(): void;
  setCameraPose(p: { position: THREE.Vector3; target: THREE.Vector3 }): void;
  setCameraDriver(driver: CameraDriver | null): void;
  canvas: HTMLCanvasElement;
}

// The camera glides toward a requested pose at this rate and snaps once the
// remaining gap stops showing, so an idle scene stops asking for 60 fps.
const POSE_RATE = 8;
const POSE_EPSILON = 0.002;

// The pointer turn follows the pointer at this rate rather than snapping.
const POINTER_RATE = 6;

// Bloom is tuned for a dark scene: a low threshold blooms the whole floor
// and the sky along with the core, which washes the frame out. Only the
// core and the ring edges are hot enough to pass this threshold.
const BLOOM = { strength: 1.1, radius: 0.6, threshold: 0.9 };

// The power-up is a page-load event, not a scene-instance one: a scene that
// remounts after a step down, an effects restore or a recovered context
// loss opens at the end state instead of flashing the grid dark again.
let booted = false;

export function mountScene(
  canvas: HTMLCanvasElement,
  initial: SceneInput,
  hooks: { onStepDown(level: EffectsLevel): void; onFail(): void },
): SceneHandle {
  const colors = readColors();
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    alpha: false,
    powerPreference: 'high-performance',
  });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.toneMapping = THREE.ACESFilmicToneMapping;

  const scene = new THREE.Scene();
  scene.fog = new THREE.FogExp2(colors.haze, 0.045);

  const camera = new THREE.PerspectiveCamera(HOME.fov, 1, 0.1, 100);
  camera.position.copy(HOME.position);
  const lookTarget = HOME.target.clone();
  camera.lookAt(lookTarget);

  const world = createWorld(colors, sceneSeed());
  const core = createCore(colors);
  scene.add(world.group, core.group);
  const bursts = createBursts(colors, core.emissive.getWorldPosition(new THREE.Vector3()));
  scene.add(bursts.group);
  world.mirror(core.emissive);

  const composer = new EffectComposer(renderer);
  composer.addPass(new RenderPass(scene, camera));
  const bloom = new UnrealBloomPass(new THREE.Vector2(1, 1), BLOOM.strength, BLOOM.radius, BLOOM.threshold);
  const setBloomSize = bloom.setSize.bind(bloom);
  // Bloom is the expensive pass and its glow hides the softer blur, so it
  // runs at half resolution.
  bloom.setSize = (width: number, height: number) => setBloomSize(width / 2, height / 2);
  composer.addPass(bloom);
  const output = new OutputPass();
  composer.addPass(output);

  let input = initial;
  core.setMode(input.mode);
  core.setFill(input.fill);
  world.setEffects(input.effects);

  const firstBoot = !booted;
  booted = true;
  let bootMs = firstBoot ? 0 : BOOT_MS;
  let bootDone = false;

  const applyBoot = (ms: number): boolean => {
    const frame = bootFrame(ms, input.motion);
    world.setReveal(frame.reveal);
    core.setBoot({ rings: frame.rings, ignite: frame.ignite });
    return frame.done;
  };
  bootDone = applyBoot(bootMs);

  // The ignition is the boot's last phase. While the session is still
  // linking the clock waits at its start, so the core lights up as the
  // overlay goes instead of before it.
  const bootHeld = () => input.booting && bootMs >= BOOT_IGNITE_MS;
  const bootRunning = () => !bootDone && !bootHeld();

  // The guard starts where an earlier scene left the effects, so a remount
  // after a step down cannot re-enable beams.
  const guard = new FrameGuard(effectsLevel.value);
  let driver: CameraDriver | null = null;
  const poseTarget = { position: HOME.position.clone(), target: HOME.target.clone() };
  const pointerTarget = { x: 0, y: 0 };
  const pointer = { x: 0, y: 0 };
  let appliedView: { x: number; y: number; width: number; height: number } | null = null;
  let poseEasing = false;
  let coreAnimating = false;
  let burstsAlive = false;
  let eventActive = false;
  let needsRender = false;
  let disposed = false;
  let raf = 0;
  let lastRender = performance.now();
  let elapsed = 0;

  const schedule = () => {
    if (!disposed && !raf) raf = requestAnimationFrame(frame);
  };

  const kick = () => {
    if (disposed) return;
    needsRender = true;
    schedule();
  };

  const applyViewOffset = () => {
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    if (!width || !height) return;
    const offset = viewOffset({ width, height }, input.focus);
    if (offset.x === 0 && offset.y === 0) {
      if (appliedView) camera.clearViewOffset();
      appliedView = null;
      return;
    }
    const same =
      appliedView !== null &&
      appliedView.x === offset.x &&
      appliedView.y === offset.y &&
      appliedView.width === width &&
      appliedView.height === height;
    if (same) return;
    camera.setViewOffset(width, height, offset.x, offset.y, width, height);
    appliedView = { x: offset.x, y: offset.y, width, height };
  };

  const followCamera = (dt: number) => {
    const pointerEase = 1 - Math.exp(-dt * POINTER_RATE);
    pointer.x += (pointerTarget.x - pointer.x) * pointerEase;
    pointer.y += (pointerTarget.y - pointer.y) * pointerEase;

    if (poseEasing) {
      const ease = 1 - Math.exp(-dt * POSE_RATE);
      camera.position.lerp(poseTarget.position, ease);
      lookTarget.lerp(poseTarget.target, ease);
      const arrived =
        camera.position.distanceTo(poseTarget.position) < POSE_EPSILON &&
        lookTarget.distanceTo(poseTarget.target) < POSE_EPSILON;
      if (arrived) {
        camera.position.copy(poseTarget.position);
        lookTarget.copy(poseTarget.target);
        poseEasing = false;
      }
    }

    camera.lookAt(lookTarget);
    const turn = parallax(pointer, input.motion);
    camera.rotation.y += turn.yaw;
    camera.rotation.x += turn.pitch;
    applyViewOffset();
  };

  function frame(now: number) {
    raf = 0;
    if (disposed) return;
    const forced = needsRender;
    needsRender = false;
    if (document.hidden) return;
    const motion = input.motion;
    if (!forced) {
      const interval = frameInterval({ hidden: false, motion, eventActive });
      if (interval === null) return;
      // Ambient frames run at 30 fps, so skip a display frame that arrives
      // too soon after the last render.
      if (!eventActive && now - lastRender < interval) {
        schedule();
        return;
      }
    }

    const dt = clampStep(now - lastRender);
    lastRender = now;
    elapsed += dt;

    if (bootRunning()) {
      bootMs = Math.min(BOOT_MS, bootMs + dt * 1000);
      bootDone = applyBoot(bootMs);
    }

    const workStart = performance.now();
    coreAnimating = core.update(dt, elapsed, motion);
    burstsAlive = bursts.update(dt);
    // The core runs first so the reflection copies this frame's pose.
    world.update(dt * motionScale(motion), elapsed);
    if (driver) driver(camera, dt);
    else followCamera(dt);
    if (input.effects.bloom) composer.render();
    else renderer.render(scene, camera);
    const workMs = performance.now() - workStart;

    // Sampling after the last step is pointless: no flag is left to lower.
    if (input.effects.bloom || input.effects.beams || input.effects.particleShare > 0.5) {
      const level = guard.sample(workMs, now);
      if (level !== null) hooks.onStepDown(level);
    }

    eventActive =
      burstsAlive ||
      coreAnimating ||
      input.mode === 'working' ||
      poseEasing ||
      driver !== null ||
      bootRunning();
    if (frameInterval({ hidden: document.hidden, motion, eventActive }) !== null) schedule();
  }

  const resize = () => {
    if (disposed) return;
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    if (!width || !height) return;
    renderer.setSize(width, height, false);
    composer.setSize(width, height);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    kick();
  };
  const observer = new ResizeObserver(resize);
  observer.observe(canvas);

  const onPointerMove = (e: PointerEvent) => {
    pointerTarget.x = (e.clientX / Math.max(1, window.innerWidth)) * 2 - 1;
    pointerTarget.y = (e.clientY / Math.max(1, window.innerHeight)) * 2 - 1;
  };
  window.addEventListener('pointermove', onPointerMove);

  const onVisibility = () => kick();
  document.addEventListener('visibilitychange', onVisibility);

  const stopWatching = watchContextLoss(canvas, hooks.onFail);

  const update = (next: SceneInput) => {
    // Bloom is only off when the level stepped down, so this edge is the
    // restore and the cooldown restarts from here.
    const restored = next.effects.bloom && !input.effects.bloom;
    if (next.mode !== input.mode) core.setMode(next.mode);
    if (next.fill !== input.fill) core.setFill(next.fill);
    input = next;
    world.setEffects(next.effects);
    if (restored) guard.reset(0, performance.now());
    kick();
  };

  const burst = (c: ToolCategory) => {
    if (input.motion === 'full') bursts.spawn(c);
    else if (input.motion === 'reduced') core.flash(categoryColor(colors, c));
    kick();
  };

  const setCameraPose = (p: { position: THREE.Vector3; target: THREE.Vector3 }) => {
    poseTarget.position.copy(p.position);
    poseTarget.target.copy(p.target);
    poseEasing = true;
    kick();
  };

  const setCameraDriver = (next: CameraDriver | null) => {
    driver = next;
    // The driver owns the camera, including any offset it decides on.
    if (next) {
      camera.clearViewOffset();
      appliedView = null;
    }
    kick();
  };

  const dispose = () => {
    if (disposed) return;
    disposed = true;
    cancelAnimationFrame(raf);
    raf = 0;
    observer.disconnect();
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('pointermove', onPointerMove);
    stopWatching();
    bursts.dispose();
    core.dispose();
    world.dispose();
    bloom.dispose();
    output.dispose();
    composer.dispose();
    renderer.dispose();
  };

  resize();

  return {
    update,
    burst,
    dispose,
    camera,
    world: scene,
    requestFrame: kick,
    setCameraPose,
    setCameraDriver,
    canvas,
  };
}

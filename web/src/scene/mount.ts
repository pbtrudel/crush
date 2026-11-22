import * as THREE from 'three';
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js';
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js';
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js';
import { UnrealBloomPass } from 'three/addons/postprocessing/UnrealBloomPass.js';
import { FLIGHT_MS } from '../state/cinematic';
import { effectsLevel, type EffectFlags, type EffectsLevel } from '../state/effects';
import type { AgentScreen } from '../state/layout';
import type { CoreMode } from '../state/levels';
import type { Motion } from '../state/motion';
import type { ToolCategory } from '../state/toolCategory';
import { createAgentNodes, type SceneAgent } from './agents';
import { BOOT_IGNITE_MS, BOOT_MS, bootFrame } from './boot';
import { categoryColor, createBursts } from './bursts';
import { HOME, parallax, viewOffset } from './camera';
import { readColors } from './colors';
import { watchContextLoss } from './contextLoss';
import { createCore } from './core';
import { FrameGuard } from './guard';
import { sameAgentList, sameSceneInput } from './input';
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
  agents: SceneAgent[];
  selected: string | null;
}

export interface SceneHooks {
  onStepDown(level: EffectsLevel): void;
  onFail(): void;
  onAgentScreens?(screens: AgentScreen[]): void;
}

// A driver returns true while it is still flying or fading, so the mount
// keeps rendering at the event rate in every motion level.
export type CameraDriver = (camera: THREE.PerspectiveCamera, dt: number) => boolean;

export interface CameraPoseOptions {
  // Jump instead of easing: the cinematic panel cross-fade covers the cut.
  snap?: boolean;
}

export interface SceneHandle {
  update(s: SceneInput): void;
  burst(c: ToolCategory, target?: string): void;
  dispose(): void;
  camera: THREE.PerspectiveCamera;
  world: THREE.Scene;
  requestFrame(): void;
  setCameraPose(p: { position: THREE.Vector3; target: THREE.Vector3 }, o?: CameraPoseOptions): void;
  setCameraDriver(driver: CameraDriver | null): void;
  canvas: HTMLCanvasElement;
}

// The camera glides toward a requested pose at this rate and snaps once the
// remaining gap stops showing, so an idle scene stops asking for 60 fps.
const POSE_RATE = 8;
const POSE_EPSILON = 0.002;

// The pointer turn follows the pointer at this rate rather than snapping.
const POINTER_RATE = 6;

// Bloom is tuned for a dark scene. This threshold sits below the core and
// the ring edges, so they halo, and above the floor grid and the sky, which
// would wash the frame out if they bloomed. Lower it and the grid lines
// start to glow.
const BLOOM = { strength: 1.1, radius: 0.6, threshold: 0.7 };

// The power-up is a page-load event, not a scene-instance one: a scene that
// remounts after a step down, an effects restore or a recovered context
// loss opens at the end state instead of flashing the grid dark again.
let booted = false;

// Test-only: re-arms the power-up so a test can watch it run or skip it.
export function resetBootLatch(): void {
  booted = false;
}

export function mountScene(
  canvas: HTMLCanvasElement,
  initial: SceneInput,
  hooks: SceneHooks,
): SceneHandle {
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    alpha: false,
    powerPreference: 'high-performance',
  });
  try {
    return mountWith(renderer, canvas, initial, hooks);
  } catch (error) {
    // A mount that fails after the renderer exists must not strand a WebGL
    // context: the scene layer retries and browsers cap live contexts.
    renderer.dispose();
    renderer.forceContextLoss();
    throw error;
  }
}

function mountWith(
  renderer: THREE.WebGLRenderer,
  canvas: HTMLCanvasElement,
  initial: SceneInput,
  hooks: SceneHooks,
): SceneHandle {
  const colors = readColors();
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
  const agents = createAgentNodes(colors, core.emissive.getWorldPosition(new THREE.Vector3()));
  scene.add(agents.group);
  agents.sync(initial.agents, initial.selected);
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
  // The focus offset belongs to the normal layout. Cinematic mode centers the
  // scene, and blending the offset in and out keeps the projector from
  // jumping sideways when the flight starts or ends.
  let viewBlend = 1;
  let viewTarget = 1;
  let viewEasing = false;
  let poseEasing = false;
  let driverBusy = false;
  let coreAnimating = false;
  let burstsAlive = false;
  let nodesAnimating = false;
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
    const x = offset.x * viewBlend;
    const y = offset.y * viewBlend;
    if (Math.abs(x) < 0.5 && Math.abs(y) < 0.5) {
      if (appliedView) camera.clearViewOffset();
      appliedView = null;
      return;
    }
    const same =
      appliedView !== null &&
      appliedView.x === x &&
      appliedView.y === y &&
      appliedView.width === width &&
      appliedView.height === height;
    if (same) return;
    camera.setViewOffset(width, height, x, y, width, height);
    appliedView = { x, y, width, height };
  };

  // stepViewBlend walks the offset toward its target over the flight, the
  // same span the camera takes to fly, and reports whether it is still
  // moving so the frame loop keeps going.
  const stepViewBlend = (dt: number): boolean => {
    if (viewBlend === viewTarget) return false;
    const step = dt / (FLIGHT_MS / 1000);
    viewBlend =
      viewTarget > viewBlend
        ? Math.min(viewTarget, viewBlend + step)
        : Math.max(viewTarget, viewBlend - step);
    return viewBlend !== viewTarget;
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
  };

  // A busy driver is the cinematic transition: it must render at the event
  // rate in every motion level. An idle one leaves the scene ambience in
  // charge, which is 30 fps in full and reduced and kick-only in off.
  const renderInterval = (): number | null => {
    if (driverBusy) return frameInterval({ hidden: document.hidden, motion: 'full', eventActive: true });
    return frameInterval({ hidden: document.hidden, motion: input.motion, eventActive });
  };

  let lastScreenKey = '';
  const projected = new THREE.Vector3();
  // publishScreens projects every node after a render and reports only a
  // change of at least a pixel, so idle frames cost the page nothing.
  const publishScreens = () => {
    if (!hooks.onAgentScreens) return;
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    const screens: AgentScreen[] = [];
    for (const id of agents.ids()) {
      if (!agents.position(id, projected)) continue;
      projected.project(camera);
      screens.push({
        id,
        x: Math.round(((projected.x + 1) / 2) * width),
        y: Math.round(((1 - projected.y) / 2) * height),
        visible: projected.z < 1,
      });
    }
    const key = JSON.stringify(screens);
    if (key === lastScreenKey) return;
    lastScreenKey = key;
    hooks.onAgentScreens(screens);
  };

  function frame(now: number) {
    raf = 0;
    if (disposed) return;
    const forced = needsRender;
    needsRender = false;
    if (document.hidden) return;
    const motion = input.motion;
    if (!forced) {
      const interval = renderInterval();
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
      // While the session is still linking the clock waits at the ignition,
      // so it must not overshoot it by the length of a frame.
      const cap = input.booting ? BOOT_IGNITE_MS : BOOT_MS;
      bootMs = Math.min(cap, bootMs + dt * 1000);
      bootDone = applyBoot(bootMs);
    }

    const workStart = performance.now();
    coreAnimating = core.update(dt, elapsed, motion);
    burstsAlive = bursts.update(dt);
    nodesAnimating = agents.update(dt, elapsed, motion);
    // The core runs first so the reflection copies this frame's pose.
    world.update(dt * motionScale(motion), elapsed);
    // The offset moves before the driver runs: a cinematic driver projects
    // the log from the projection matrix inside its own frame, so it has to
    // read this frame's offset rather than the last frame's.
    viewEasing = stepViewBlend(dt);
    applyViewOffset();
    if (driver) driverBusy = driver(camera, dt);
    else {
      driverBusy = false;
      followCamera(dt);
    }
    if (input.effects.bloom) composer.render();
    else renderer.render(scene, camera);
    publishScreens();
    const workMs = performance.now() - workStart;

    // Sampling after the last step is pointless: no flag is left to lower.
    if (input.effects.bloom || input.effects.beams || input.effects.particleShare > 0.5) {
      const level = guard.sample(workMs, now);
      if (level !== null) hooks.onStepDown(level);
    }

    eventActive =
      burstsAlive ||
      nodesAnimating ||
      coreAnimating ||
      input.mode === 'working' ||
      poseEasing ||
      driverBusy ||
      viewEasing ||
      bootRunning();
    if (renderInterval() !== null) schedule();
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
    if (sameSceneInput(next, input)) return;
    // Bloom is only off when the level stepped down, so this edge is the
    // restore and the cooldown restarts from here.
    const restored = next.effects.bloom && !input.effects.bloom;
    const rosterChanged = next.selected !== input.selected || !sameAgentList(next.agents, input.agents);
    if (next.mode !== input.mode) core.setMode(next.mode);
    if (next.fill !== input.fill) core.setFill(next.fill);
    input = next;
    world.setEffects(next.effects);
    if (rosterChanged) agents.sync(next.agents, next.selected);
    if (restored) guard.reset(0, performance.now());
    kick();
  };

  const burstTarget = new THREE.Vector3();
  const burst = (c: ToolCategory, agentId?: string) => {
    const color = categoryColor(colors, c);
    if (agentId && agents.position(agentId, burstTarget)) {
      if (input.motion === 'full') {
        bursts.spawnAt(c, burstTarget);
        agents.pulse(agentId, color);
      } else if (input.motion === 'reduced') {
        agents.flash(agentId, color);
      }
    } else if (input.motion === 'full') {
      bursts.spawn(c);
    } else if (input.motion === 'reduced') {
      core.flash(color);
    }
    kick();
  };

  const setCameraPose = (p: { position: THREE.Vector3; target: THREE.Vector3 }, options?: CameraPoseOptions) => {
    poseTarget.position.copy(p.position);
    poseTarget.target.copy(p.target);
    if (options?.snap) {
      camera.position.copy(p.position);
      lookTarget.copy(p.target);
      poseEasing = false;
    } else {
      poseEasing = true;
    }
    kick();
  };

  const setCameraDriver = (next: CameraDriver | null) => {
    driver = next;
    driverBusy = false;
    if (next) {
      // Cinematic mode centers the scene: ease the focus offset away over
      // the flight so the projector does not jump sideways.
      viewTarget = 0;
    } else {
      // The driver left the camera looking along its orbit heading. Seed the
      // idle look target from that heading so the fly home does not snap.
      const direction = camera.getWorldDirection(new THREE.Vector3());
      lookTarget.copy(camera.position).add(direction.multiplyScalar(camera.position.distanceTo(HOME.target)));
      viewTarget = 1;
    }
    if (input.motion !== 'full') viewBlend = viewTarget;
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
    agents.dispose();
    core.dispose();
    world.dispose();
    bloom.dispose();
    output.dispose();
    composer.dispose();
    renderer.dispose();
    // Releasing the drawing buffer is what frees the context slot; without
    // it a remount can exhaust the browser's context limit.
    renderer.forceContextLoss();
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

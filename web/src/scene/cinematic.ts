import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { FADE_MS, FLIGHT_MS } from '../state/cinematic';
import type { Motion } from '../state/motion';
import { HOME, WIDE } from './camera';
import { cameraCSS, objectCSS } from './cssProjection';
import { expoOut } from './ease';
import type { SceneHandle } from './mount';

// The log hangs to the left of the projector, turned toward the wide orbit,
// with one CSS pixel standing for 0.008 world units: at the wide camera the
// panel lands at about 0.87x of its CSS size, so body text stays legible.
// The anchor clears the stage column, which keeps the agent chip off the
// panel's top-right corner.
const LOG_ANCHOR = { x: -3.5, y: 1.8, z: 0.6, yaw: 0.55, scale: 0.008 };

// The mount snaps its own pose easing inside this distance, so the fly-back
// is over by then.
const HOME_EPSILON = 0.01;

export interface CinematicEls {
  view: HTMLElement;
  camera: HTMLElement;
  panels: HTMLElement[];
}

export interface CinematicHandle {
  stop(): Promise<void>;
}

// One anchor per panel, all at the log's place: M1 projects the log alone.
function anchorsFor(panels: HTMLElement[]): THREE.Object3D[] {
  return panels.map(() => {
    const anchor = new THREE.Object3D();
    anchor.position.set(LOG_ANCHOR.x, LOG_ANCHOR.y, LOG_ANCHOR.z);
    anchor.rotation.y = LOG_ANCHOR.yaw;
    anchor.scale.setScalar(LOG_ANCHOR.scale);
    anchor.updateMatrixWorld();
    return anchor;
  });
}

// The mount eases the camera home at its own rate; wait for it to arrive,
// with a cap so a scene disposed mid-flight cannot leave the promise
// pending.
function arrivedHome(camera: THREE.PerspectiveCamera): Promise<void> {
  return new Promise((resolve) => {
    const deadline = performance.now() + FLIGHT_MS * 2;
    const check = () => {
      if (camera.position.distanceTo(HOME.position) < HOME_EPSILON || performance.now() >= deadline) {
        resolve();
        return;
      }
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  });
}

export function startCinematic(scene: SceneHandle, els: CinematicEls, motion: Motion): CinematicHandle {
  const camera = scene.camera;
  const anchors = anchorsFor(els.panels);
  // The flight starts from where the camera actually looks: the pointer
  // parallax can leave it a couple of degrees off the idle target.
  const from = {
    position: camera.position.clone(),
    target: camera.position.clone().add(
      camera.getWorldDirection(new THREE.Vector3()).multiplyScalar(camera.position.distanceTo(HOME.target)),
    ),
  };

  // Reduced and off motion jump instead of flying, and cross-fade the panels
  // in over FADE_MS; full motion lets the flight be the entrance and reveals
  // them whole on the first frame.
  const jumped = motion !== 'full';
  if (jumped) camera.position.copy(WIDE.position);
  let flightMs = jumped ? FLIGHT_MS : 0;
  let fadeMs = jumped ? 0 : FADE_MS;

  const controls = new OrbitControls(camera, scene.canvas);
  controls.enablePan = false;
  // The polar limits keep the camera above the floor; the distance limits
  // keep the log readable.
  controls.minPolarAngle = 0.35;
  controls.maxPolarAngle = 1.35;
  controls.minDistance = 6;
  controls.maxDistance = 18;
  controls.target.copy(jumped ? WIDE.target : from.target);
  // Dragging or zooming changes the camera without a state change, so the
  // scene needs a frame for each of them in every motion level.
  controls.addEventListener('change', scene.requestFrame);

  // The keyboard path covers cinematic mode: the canvas itself takes the
  // focus ring, and OrbitControls turns shift plus arrows into orbit. The
  // arrows in the prompt never reach the canvas.
  controls.listenToKeyEvents(scene.canvas);
  scene.canvas.tabIndex = 0;
  scene.canvas.setAttribute('aria-label', '3D view, shift plus arrow keys orbit');
  scene.canvas.removeAttribute('aria-hidden');

  const write = () => {
    const width = els.view.clientWidth || window.innerWidth;
    const height = els.view.clientHeight || window.innerHeight;
    // The CSS reads both matrices and the mount renders after the driver,
    // so they are brought up to date here rather than a frame late.
    camera.updateMatrixWorld();
    els.camera.style.transform = cameraCSS(camera, width, height);
    els.panels.forEach((panel, i) => {
      panel.style.transform = objectCSS(anchors[i]);
    });
  };

  const driver = (cam: THREE.PerspectiveCamera, dt: number): boolean => {
    if (flightMs < FLIGHT_MS) {
      flightMs = Math.min(FLIGHT_MS, flightMs + dt * 1000);
      const t = expoOut(flightMs / FLIGHT_MS);
      cam.position.lerpVectors(from.position, WIDE.position, t);
      controls.target.lerpVectors(from.target, WIDE.target, t);
      // OrbitControls' first update would clamp the camera into the polar
      // limits and pull the flight off its path, so the flight aims the
      // camera itself and hands over once it lands.
      cam.lookAt(controls.target);
    } else {
      controls.update();
    }
    fadeMs = Math.min(FADE_MS, fadeMs + dt * 1000);
    els.view.style.opacity = String(fadeMs / FADE_MS);
    write();
    return flightMs < FLIGHT_MS || fadeMs < FADE_MS;
  };

  const stop = (): Promise<void> => {
    controls.removeEventListener('change', scene.requestFrame);
    controls.dispose();
    scene.canvas.removeAttribute('tabindex');
    scene.canvas.removeAttribute('aria-label');
    scene.canvas.setAttribute('aria-hidden', 'true');
    // The mount and the controls must never both own the camera: the driver
    // goes first, then the mount takes the camera back. Reduced and off
    // motion snap home instead of flying.
    scene.setCameraDriver(null);
    scene.setCameraPose(
      { position: HOME.position.clone(), target: HOME.target.clone() },
      { snap: motion !== 'full' },
    );
    els.camera.style.transform = '';
    for (const panel of els.panels) panel.style.transform = '';
    els.view.style.opacity = '';
    return arrivedHome(camera);
  };

  scene.setCameraDriver(driver);
  return { stop };
}

import * as THREE from 'three';
import type { Motion } from '../state/motion';

// HOME frames the projector for the normal layout; WIDE is the cinematic
// pull-back that leaves room for the panels.
export const HOME = {
  position: new THREE.Vector3(0, 2.2, 7.4),
  target: new THREE.Vector3(0, 1.0, 0),
  fov: 42,
};

export const WIDE = {
  position: new THREE.Vector3(0, 4.6, 11.5),
  target: new THREE.Vector3(0, 1.2, 0),
};

interface Viewport { width: number; height: number }
interface FocusRect { x: number; y: number; width: number; height: number }

// viewOffset moves the image the other way, so the projector lands in the
// middle of the focus rect instead of the middle of the canvas. The caller
// feeds it to camera.setViewOffset and clears it when the result is zero.
export function viewOffset(viewport: Viewport, focus: FocusRect | null): { x: number; y: number } {
  if (!focus) return { x: 0, y: 0 };
  return {
    x: viewport.width / 2 - (focus.x + focus.width / 2),
    y: viewport.height / 2 - (focus.y + focus.height / 2),
  };
}

const MAX_TILT = (2 * Math.PI) / 180;

const clampAxis = (value: number) => Math.max(-1, Math.min(1, value));

// parallax turns the pointer into the small head-turn the camera follows.
// Reduced and off motion keep the camera still, so nothing drifts.
export function parallax(pointer: { x: number; y: number }, motion: Motion): { yaw: number; pitch: number } {
  if (motion !== 'full') return { yaw: 0, pitch: 0 };
  return {
    yaw: clampAxis(pointer.x) * MAX_TILT,
    pitch: clampAxis(pointer.y) * MAX_TILT,
  };
}

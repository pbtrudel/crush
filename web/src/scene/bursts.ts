import * as THREE from 'three';
import type { ToolCategory } from '../state/toolCategory';
import type { SceneColors } from './colors';
import { expoOut } from './ease';

export const BURST_MS = 900;
export const MAX_BURSTS = 12;

// A ring starts a third of its final size, so the first frame already
// rings the core instead of hiding inside the crystal.
const START_SCALE = 0.3;
const END_SCALE = 2.6;
const START_OPACITY = 0.9;

const BURST_SECONDS = BURST_MS / 1000;

export function categoryColor(colors: SceneColors, c: ToolCategory): THREE.Color {
  if (c === 'read') return colors.cyan;
  if (c === 'write') return colors.teal;
  if (c === 'run') return colors.amber;
  return colors.violet;
}

interface Ring {
  mesh: THREE.Mesh;
  material: THREE.MeshBasicMaterial;
  age: number;
  // born orders the pool so a spawn at the cap recycles the oldest ring.
  born: number;
}

export interface Bursts {
  group: THREE.Group;
  spawn(c: ToolCategory): void;
  spawnAt(c: ToolCategory, at: THREE.Vector3): void;
  update(dt: number): boolean;
  active(): number;
  dispose(): void;
}

export function createBursts(colors: SceneColors, origin: THREE.Vector3): Bursts {
  const group = new THREE.Group();
  group.position.copy(origin);

  // The whole pool shares one geometry; only the color and the age differ.
  const geometry = new THREE.RingGeometry(0.96, 1, 96);
  const rings: Ring[] = [];
  for (let i = 0; i < MAX_BURSTS; i++) {
    const material = new THREE.MeshBasicMaterial({
      color: colors.cyan.clone(),
      transparent: true,
      opacity: 0,
      // The cinematic camera orbits, so a flat ring must show from behind.
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    });
    const mesh = new THREE.Mesh(geometry, material);
    mesh.visible = false;
    group.add(mesh);
    rings.push({ mesh, material, age: 0, born: 0 });
  }

  let spawns = 0;

  const spawn = (c: ToolCategory) => {
    const free = rings.find((r) => !r.mesh.visible);
    const ring = free ?? rings.reduce((a, b) => (b.born < a.born ? b : a));
    ring.age = 0;
    ring.born = spawns++;
    ring.material.color.copy(categoryColor(colors, c));
    ring.material.opacity = START_OPACITY;
    ring.mesh.scale.setScalar(START_SCALE);
    ring.mesh.position.set(0, 0, 0);
    ring.mesh.visible = true;
  };

  // spawnAt rings a node instead of the core. The pool lives in the core's
  // frame, so the node position is made relative to it.
  const spawnAt = (c: ToolCategory, at: THREE.Vector3) => {
    spawn(c);
    const ring = rings.reduce((a, b) => (b.born > a.born ? b : a));
    ring.mesh.position.copy(at).sub(group.position);
  };

  const update = (dt: number): boolean => {
    let alive = 0;
    for (const ring of rings) {
      if (!ring.mesh.visible) continue;
      ring.age += dt;
      if (ring.age >= BURST_SECONDS) {
        ring.mesh.visible = false;
        continue;
      }
      const p = expoOut(ring.age / BURST_SECONDS);
      ring.mesh.scale.setScalar(START_SCALE + (END_SCALE - START_SCALE) * p);
      ring.material.opacity = START_OPACITY * (1 - p);
      alive++;
    }
    return alive > 0;
  };

  const active = () => rings.filter((r) => r.mesh.visible).length;

  const dispose = () => {
    geometry.dispose();
    for (const ring of rings) ring.material.dispose();
    // A disposed pool must not leave its meshes behind in the scene graph.
    group.clear();
  };

  return { group, spawn, spawnAt, update, active, dispose };
}

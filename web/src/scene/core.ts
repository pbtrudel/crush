import * as THREE from 'three';
import { contextLevel, type ContextLevel, type CoreMode } from '../state/levels';
import type { Motion } from '../state/motion';
import type { SceneColors } from './colors';
import { motionScale } from './motionScale';

export interface CoreLook {
  color: 'cyan' | 'magenta';
  spin: number;
  pulsePeriod: number;
  intensity: number;
  flicker: boolean;
}

const LOOKS: Record<CoreMode, CoreLook> = {
  idle: { color: 'cyan', spin: 0.15, pulsePeriod: 4, intensity: 1, flicker: false },
  working: { color: 'cyan', spin: 0.9, pulsePeriod: 1.2, intensity: 1.6, flicker: false },
  attention: { color: 'magenta', spin: 0.3, pulsePeriod: 1, intensity: 1.4, flicker: false },
  error: { color: 'magenta', spin: 0.15, pulsePeriod: 0, intensity: 1.1, flicker: true },
};

export function coreLook(mode: CoreMode): CoreLook {
  return LOOKS[mode];
}

// A lerp stalls a few ulp short of the target, so without a tolerance it
// would never report settled; snap once the remaining gap stops showing.
const SETTLE_EPSILON = 0.001;

function settled(current: THREE.Color, target: THREE.Color): boolean {
  return (
    Math.abs(target.r - current.r) < SETTLE_EPSILON &&
    Math.abs(target.g - current.g) < SETTLE_EPSILON &&
    Math.abs(target.b - current.b) < SETTLE_EPSILON
  );
}

// stepColor moves a colour toward the attention target. Full motion blends
// over several frames, but reduced and off render one frame per update, so
// they snap: the user who asked for less motion still sees the cue at once.
export function stepColor(current: THREE.Color, target: THREE.Color, motion: Motion): void {
  if (motion !== 'full') {
    current.copy(target);
    return;
  }
  current.lerp(target, 0.2);
  if (settled(current, target)) current.copy(target);
}

export interface Core {
  group: THREE.Group;
  emissive: THREE.Object3D;
  setMode(m: CoreMode): void;
  setFill(fill: number): void;
  flash(color: THREE.Color): void;
  setBoot(p: { rings: number; ignite: number }): void;
  update(dt: number, t: number, motion: Motion): boolean;
  dispose(): void;
}

// The core floats above the pedestal; the camera is framed around it.
const CORE_HEIGHT = 1.6;

const FLASH_SECONDS = 0.6;
const FLICKER_SECONDS = 0.09;

// A fill change below this is not worth rebuilding the ring geometry for.
const FILL_STEP = 0.001;

const HEART_OPACITY = 0.6;
const CRYSTAL_OPACITY = 0.18;
const RIM_OPACITY = 0.85;

const ringGeometry = (fill: number) =>
  new THREE.RingGeometry(0.95, 1.02, 128, 1, Math.PI / 2, Math.max(0.0001, Math.PI * 2 * fill));

function levelColor(level: ContextLevel, colors: SceneColors): THREE.Color {
  if (level === 'critical') return colors.magenta;
  if (level === 'warn') return colors.amber;
  return colors.cyan;
}

export function createCore(colors: SceneColors): Core {
  const group = new THREE.Group();
  const emissive = new THREE.Group();
  emissive.name = 'emissive';

  const projector = new THREE.Group();
  projector.name = 'projector';
  const pedestal: THREE.Mesh[] = [];
  [1.1, 0.9, 0.7].forEach((radius, i) => {
    const mesh = new THREE.Mesh(
      new THREE.TorusGeometry(radius, 0.03, 12, 96),
      new THREE.MeshBasicMaterial({
        color: colors.cyan,
        transparent: true,
        opacity: 0.8 - i * 0.15,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      }),
    );
    mesh.name = 'pedestal-ring';
    mesh.rotation.x = Math.PI / 2;
    mesh.position.y = 0.02 + i * 0.1;
    projector.add(mesh);
    pedestal.push(mesh);
  });

  const coneGeometry = new THREE.CylinderGeometry(0.35, 1.0, 1.6, 48, 1, true);
  const conePosition = coneGeometry.getAttribute('position');
  const coneColors = new Float32Array(conePosition.count * 3);
  for (let i = 0; i < conePosition.count; i++) {
    // The beam leaves the pedestal at full strength and dies out at the
    // top, where it meets the core.
    const fade = 0.2 + 0.8 * (1 - (conePosition.getY(i) + 0.8) / 1.6);
    coneColors[i * 3] = colors.cyan.r * fade;
    coneColors[i * 3 + 1] = colors.cyan.g * fade;
    coneColors[i * 3 + 2] = colors.cyan.b * fade;
  }
  coneGeometry.setAttribute('color', new THREE.BufferAttribute(coneColors, 3));
  const cone = new THREE.Mesh(
    coneGeometry,
    new THREE.MeshBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: 0.08,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    }),
  );
  cone.name = 'cone';
  cone.position.y = 0.9;
  projector.add(cone);
  group.add(projector);

  const crystalGeometry = new THREE.OctahedronGeometry(0.55, 0);
  const crystalMaterial = new THREE.MeshBasicMaterial({
    color: colors.cyan.clone(),
    transparent: true,
    opacity: CRYSTAL_OPACITY,
    side: THREE.DoubleSide,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const crystal = new THREE.Mesh(crystalGeometry, crystalMaterial);
  crystal.name = 'crystal';
  crystal.scale.set(1, 1.4, 1);

  const rimMaterial = new THREE.LineBasicMaterial({
    color: colors.cyan.clone(),
    transparent: true,
    opacity: RIM_OPACITY,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const rim = new THREE.LineSegments(new THREE.EdgesGeometry(crystalGeometry), rimMaterial);
  rim.name = 'rim';

  const heartMaterial = new THREE.MeshBasicMaterial({
    color: colors.cyan.clone(),
    transparent: true,
    opacity: HEART_OPACITY,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const heart = new THREE.Mesh(new THREE.IcosahedronGeometry(0.18, 2), heartMaterial);
  heart.name = 'heart';

  emissive.add(crystal, rim, heart);
  emissive.position.y = CORE_HEIGHT;
  group.add(emissive);

  const ringMaterial = new THREE.MeshBasicMaterial({
    color: colors.cyan.clone(),
    transparent: true,
    opacity: 0.9,
    side: THREE.DoubleSide,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const contextRing = new THREE.Mesh(ringGeometry(0), ringMaterial);
  contextRing.name = 'context-ring';
  contextRing.position.y = CORE_HEIGHT;
  group.add(contextRing);

  const flashColor = colors.teal.clone();
  let mode: CoreMode = 'idle';
  let fill = 0;
  let flashTime = FLASH_SECONDS;
  let flickerClock = 0;
  let ignite = 1;

  const setMode = (m: CoreMode) => {
    mode = m;
    flickerClock = 0;
    rim.visible = true;
  };

  const setFill = (next: number) => {
    const value = Math.max(0, Math.min(1, next));
    if (Math.abs(value - fill) <= FILL_STEP) return;
    fill = value;
    contextRing.geometry.dispose();
    contextRing.geometry = ringGeometry(fill);
    ringMaterial.color.copy(levelColor(contextLevel(fill), colors));
  };

  const flash = (color: THREE.Color) => {
    flashColor.copy(color);
    flashTime = 0;
  };

  const setBoot = (p: { rings: number; ignite: number }) => {
    ignite = Math.max(0, Math.min(1, p.ignite));
    pedestal.forEach((mesh, i) => {
      mesh.visible = i < p.rings;
    });
  };

  const update = (dt: number, t: number, motion: Motion): boolean => {
    const look = coreLook(mode);
    const target = look.color === 'cyan' ? colors.cyan : colors.magenta;

    emissive.rotation.y += look.spin * dt * motionScale(motion);

    stepColor(crystalMaterial.color, target, motion);
    stepColor(rimMaterial.color, target, motion);
    stepColor(heartMaterial.color, target, motion);

    const flashLeft = Math.max(0, 1 - flashTime / FLASH_SECONDS);
    if (flashTime < FLASH_SECONDS) flashTime += dt;

    // A flash brightens the heart through its opacity and tints the crystal
    // body, so the heart keeps the exact state colour while a flash lives.
    crystalMaterial.color.lerp(flashColor, 0.6 * flashLeft);
    crystalMaterial.opacity = CRYSTAL_OPACITY * look.intensity * ignite;
    rimMaterial.opacity = Math.min(1, RIM_OPACITY * look.intensity) * ignite;
    heartMaterial.opacity = Math.min(1, (HEART_OPACITY + 0.5 * flashLeft) * look.intensity) * ignite;

    const pulse = look.pulsePeriod > 0 ? 1 + 0.06 * Math.sin((2 * Math.PI * t) / look.pulsePeriod) : 1;
    crystal.scale.set(pulse, 1.4 * pulse, pulse);

    if (look.flicker && motion === 'full') {
      flickerClock += dt;
      rim.visible = Math.floor(flickerClock / FLICKER_SECONDS) % 2 === 0;
    } else {
      flickerClock = 0;
      rim.visible = true;
    }

    const moving =
      !crystalMaterial.color.equals(target) ||
      !rimMaterial.color.equals(target) ||
      !heartMaterial.color.equals(target);
    return moving || flashLeft > 0;
  };

  const dispose = () => {
    const geometries = new Set<THREE.BufferGeometry>();
    const materials = new Set<THREE.Material>();
    group.traverse((object) => {
      const mesh = object as THREE.Mesh;
      if (mesh.geometry) geometries.add(mesh.geometry);
      const material = mesh.material;
      if (Array.isArray(material)) material.forEach((m) => materials.add(m));
      else if (material) materials.add(material);
    });
    geometries.forEach((geometry) => geometry.dispose());
    materials.forEach((material) => material.dispose());
  };

  return { group, emissive, setMode, setFill, flash, setBoot, update, dispose };
}

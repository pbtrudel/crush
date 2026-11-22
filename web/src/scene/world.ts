import * as THREE from 'three';
import type { EffectFlags } from '../state/effects';
import type { SceneColors } from './colors';
import { mulberry32 } from './rng';

export const PARTICLE_COUNT = 1500;
export const BEAM_COUNT = 4;

// The radius at which the 80 wide floor and the reflection are fully shown.
export const REVEAL_FULL = 40;

export interface World {
  group: THREE.Group;
  particles: THREE.Points;
  beams: THREE.Group;
  floor: THREE.Mesh;
  setEffects(f: EffectFlags): void;
  setReveal(r: number): void;
  update(dt: number, t: number): void;
  mirror(source: THREE.Object3D): void;
  dispose(): void;
}

interface Beam {
  material: THREE.MeshBasicMaterial;
  period: number;
  phase: number;
}

interface ColoredMaterial extends THREE.Material {
  color?: THREE.Color;
}

const PARTICLE_TOP = 6;
const clamp01 = (value: number) => Math.max(0, Math.min(1, value));

// The sky reaches void this high on the backdrop sphere, and stays void
// above it. The camera sees the sky from the horizon up to about 12 degrees
// (15 units of the sphere), and the HUD hides most of that, so the open sky
// above the panels is a narrow band. Reaching void at 8 units keeps that
// band near the void token and leaves the haze as a glow along the horizon.
export const SKY_HEIGHT = 8;

// skyMix is the share of void in the sky at height y: none at and below the
// horizon, all of it at SKY_HEIGHT and above. The backdrop sphere samples
// this per vertex, so it needs enough rings to resolve the band.
export const skyMix = (y: number): number => clamp01(y / SKY_HEIGHT);

function createBackdrop(colors: SceneColors): THREE.Mesh {
  const geometry = new THREE.SphereGeometry(60, 32, 128);
  const position = geometry.getAttribute('position');
  const vertexColors = new Float32Array(position.count * 3);
  const tint = new THREE.Color();
  for (let i = 0; i < position.count; i++) {
    // The sky fades to void; the horizon and everything below it is haze,
    // which is where the floor and the fog meet the sky.
    tint.copy(colors.haze).lerp(colors.void, skyMix(position.getY(i)));
    vertexColors[i * 3] = tint.r;
    vertexColors[i * 3 + 1] = tint.g;
    vertexColors[i * 3 + 2] = tint.b;
  }
  geometry.setAttribute('color', new THREE.BufferAttribute(vertexColors, 3));
  const material = new THREE.MeshBasicMaterial({
    vertexColors: true,
    side: THREE.BackSide,
    fog: false,
    depthWrite: false,
  });
  const mesh = new THREE.Mesh(geometry, material);
  mesh.name = 'backdrop';
  // The sky is drawn first: the floor writes no depth, so a sky drawn after
  // it would paint over the whole grid.
  mesh.renderOrder = -1;
  return mesh;
}

function createFloor(colors: SceneColors): THREE.Mesh {
  const geometry = new THREE.PlaneGeometry(80, 80);
  const material = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    uniforms: {
      uCyan: { value: colors.cyan.clone() },
      uHaze: { value: colors.haze.clone() },
      uScroll: { value: 0 },
      uReveal: { value: 100 },
    },
    vertexShader: /* glsl */ `
      varying vec3 vWorld;

      void main() {
        vec4 world = modelMatrix * vec4(position, 1.0);
        vWorld = world.xyz;
        gl_Position = projectionMatrix * viewMatrix * world;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform vec3 uCyan;
      uniform vec3 uHaze;
      uniform float uScroll;
      uniform float uReveal;
      varying vec3 vWorld;

      void main() {
        // fwidth keeps each line one pixel wide at any distance, so the grid
        // does not shimmer as it recedes.
        vec2 p = vec2(vWorld.x, vWorld.z + uScroll);
        vec2 delta = max(fwidth(p), vec2(0.0001));
        vec2 lines = abs(fract(p + 0.5) - 0.5) / delta;
        float line = 1.0 - min(min(lines.x, lines.y), 1.0);

        float radius = length(vWorld.xz);
        float near = 1.0 - smoothstep(8.0, 34.0, radius);
        float reveal = 1.0 - clamp(radius / max(uReveal, 0.001), 0.0, 1.0);
        float glow = line * near * reveal;

        // A dark glossy base that thickens into the haze at the horizon, so
        // the far floor and the sky under it are the same colour.
        vec3 base = mix(uHaze * 0.05, uHaze, smoothstep(6.0, 38.0, radius));
        vec3 color = base + mix(uHaze, uCyan, near) * glow;
        float alpha = mix(0.55, 0.95, smoothstep(2.0, 26.0, radius)) + glow * 0.35;

        gl_FragColor = vec4(color, clamp(alpha, 0.0, 1.0));
        #include <tonemapping_fragment>
        #include <colorspace_fragment>
      }
    `,
  });
  const mesh = new THREE.Mesh(geometry, material);
  mesh.name = 'floor';
  mesh.rotation.x = -Math.PI / 2;
  return mesh;
}

function createParticles(colors: SceneColors, seed: number): THREE.Points {
  const random = mulberry32(seed);
  const positions = new Float32Array(PARTICLE_COUNT * 3);
  const tints = new Float32Array(PARTICLE_COUNT * 3);
  const tint = new THREE.Color();
  for (let i = 0; i < PARTICLE_COUNT; i++) {
    positions[i * 3] = random() * 24 - 12;
    positions[i * 3 + 1] = random() * PARTICLE_TOP;
    positions[i * 3 + 2] = random() * 24 - 12;
    tint.copy(colors.cyan).lerp(colors.violet, random());
    tints[i * 3] = tint.r;
    tints[i * 3 + 1] = tint.g;
    tints[i * 3 + 2] = tint.b;
  }
  const geometry = new THREE.BufferGeometry();
  geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3));
  geometry.setAttribute('color', new THREE.BufferAttribute(tints, 3));
  const material = new THREE.PointsMaterial({
    size: 0.035,
    vertexColors: true,
    transparent: true,
    opacity: 0.8,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  return new THREE.Points(geometry, material);
}

function createBeams(colors: SceneColors, seed: number): { group: THREE.Group; list: Beam[] } {
  const group = new THREE.Group();
  const random = mulberry32(seed ^ 0x9e3779b9);
  const list: Beam[] = [];
  for (let i = 0; i < BEAM_COUNT; i++) {
    // Four width segments let the colour fall off at both sides, so the
    // shaft has soft edges instead of a visible rectangle.
    const geometry = new THREE.PlaneGeometry(1.2, 9, 4, 1);
    const position = geometry.getAttribute('position');
    const tint = i % 2 === 0 ? colors.cyan : colors.violet;
    const vertexColors = new Float32Array(position.count * 3);
    for (let v = 0; v < position.count; v++) {
      // Additive blending reads a darker vertex as a fainter one, so the
      // crown brightens toward the top and the foot fades out with no
      // alpha attribute.
      const along = (position.getX(v) + 0.6) / 1.2;
      const across = Math.sin(Math.PI * along);
      const rise = ((position.getY(v) + 4.5) / 9) ** 1.5;
      const strength = rise * across;
      vertexColors[v * 3] = tint.r * strength;
      vertexColors[v * 3 + 1] = tint.g * strength;
      vertexColors[v * 3 + 2] = tint.b * strength;
    }
    geometry.setAttribute('color', new THREE.BufferAttribute(vertexColors, 3));
    const material = new THREE.MeshBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: 0.05,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    });
    const mesh = new THREE.Mesh(geometry, material);
    // The shafts stand behind the projector so they never cover the core,
    // and their crowns lean in toward it.
    const azimuth = 2.45 + (i / (BEAM_COUNT - 1)) * (4.0 - 2.45);
    const x = Math.sin(azimuth) * 5.5;
    mesh.position.set(x, 4.5, Math.cos(azimuth) * 5.5);
    mesh.rotation.set(0.1, 0, x >= 0 ? 0.14 : -0.14);
    group.add(mesh);
    list.push({ material, period: 6 + random() * 4, phase: random() * Math.PI * 2 });
  }
  return { group, list };
}

function syncMaterial(source: THREE.Material, copy: THREE.Material, fade: number): void {
  const from = source as ColoredMaterial;
  const to = copy as ColoredMaterial;
  if (from.color && to.color) to.color.copy(from.color);
  to.opacity = from.opacity * 0.25 * fade;
}

function syncObject(source: THREE.Object3D, copy: THREE.Object3D, fade: number): void {
  copy.position.copy(source.position);
  copy.rotation.copy(source.rotation);
  copy.scale.copy(source.scale);
  copy.visible = source.visible;
  const from = (source as THREE.Mesh).material;
  const to = (copy as THREE.Mesh).material;
  if (!from || !to) return;
  if (Array.isArray(from) && Array.isArray(to)) {
    from.forEach((material, i) => {
      if (to[i]) syncMaterial(material, to[i], fade);
    });
  } else if (!Array.isArray(from) && !Array.isArray(to)) {
    syncMaterial(from, to, fade);
  }
}

export function createWorld(colors: SceneColors, seed: number): World {
  const group = new THREE.Group();
  const geometries = new Set<THREE.BufferGeometry>();
  const materials = new Set<THREE.Material>();

  const trackMaterials = (root: THREE.Object3D) => {
    root.traverse((object) => {
      const material = (object as THREE.Mesh).material;
      if (Array.isArray(material)) material.forEach((m) => materials.add(m));
      else if (material) materials.add(material);
    });
  };

  const track = (root: THREE.Object3D) => {
    root.traverse((object) => {
      const geometry = (object as THREE.Mesh).geometry;
      if (geometry) geometries.add(geometry);
    });
    trackMaterials(root);
  };

  const backdrop = createBackdrop(colors);
  const floor = createFloor(colors);
  const particles = createParticles(colors, seed);
  const beams = createBeams(colors, seed);
  [backdrop, floor, particles, beams.group].forEach(track);
  group.add(backdrop, floor, particles, beams.group);

  const mirrorGroup = new THREE.Group();
  mirrorGroup.name = 'mirror';
  // The group scale is the only flip: the clone keeps the source's local
  // transform, so a source above the floor lands below it.
  mirrorGroup.scale.y = -1;
  group.add(mirrorGroup);

  // Each entry pairs the source tree with its clone in traversal order, so
  // update can copy one onto the other without searching.
  const links: Array<Array<[THREE.Object3D, THREE.Object3D]>> = [];
  let mirrorFade = 1;

  const mirror = (source: THREE.Object3D) => {
    const copy = source.clone(true);
    copy.traverse((object) => {
      const mesh = object as THREE.Mesh;
      const material = mesh.material;
      if (Array.isArray(material)) mesh.material = material.map((m) => m.clone());
      else if (material) mesh.material = material.clone();
    });
    // The clone shares the source geometry, which the source owns; only the
    // materials are this world's to release.
    trackMaterials(copy);
    mirrorGroup.add(copy);
    const sources: THREE.Object3D[] = [];
    const copies: THREE.Object3D[] = [];
    source.traverse((object) => sources.push(object));
    copy.traverse((object) => copies.push(object));
    links.push(sources.map((object, i) => [object, copies[i]]));
  };

  return {
    group,
    particles,
    beams: beams.group,
    floor,
    setEffects(f: EffectFlags) {
      beams.group.visible = f.beams;
      particles.geometry.setDrawRange(0, Math.round(PARTICLE_COUNT * f.particleShare));
    },
    setReveal(radius: number) {
      (floor.material as THREE.ShaderMaterial).uniforms.uReveal.value = radius;
      mirrorFade = clamp01(radius / REVEAL_FULL);
    },
    update(dt: number, t: number) {
      (floor.material as THREE.ShaderMaterial).uniforms.uScroll.value += 0.15 * dt;

      const position = particles.geometry.getAttribute('position') as THREE.BufferAttribute;
      const array = position.array as Float32Array;
      for (let i = 1; i < array.length; i += 3) {
        array[i] += 0.05 * dt;
        if (array[i] > PARTICLE_TOP) array[i] -= PARTICLE_TOP;
      }
      position.needsUpdate = true;

      for (const beam of beams.list) {
        beam.material.opacity = 0.05 + 0.07 * (0.5 + 0.5 * Math.sin((2 * Math.PI * t) / beam.period + beam.phase));
      }

      for (const pairs of links) {
        for (const [source, copy] of pairs) syncObject(source, copy, mirrorFade);
      }
    },
    mirror,
    dispose() {
      geometries.forEach((geometry) => geometry.dispose());
      materials.forEach((material) => material.dispose());
      geometries.clear();
      materials.clear();
    },
  };
}

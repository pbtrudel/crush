import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { BEAM_COUNT, createWorld, PARTICLE_COUNT, REVEAL_FULL } from './world';
import { readColors } from './colors';

describe('createWorld', () => {
  it('draws every particle in one Points object', () => {
    const w = createWorld(readColors(), 7);
    let points = 0;
    w.group.traverse((o) => { if ((o as THREE.Points).isPoints) points++; });
    expect(points).toBe(1);
    expect(w.particles.geometry.getAttribute('position').count).toBe(PARTICLE_COUNT);
  });

  it('places particles the same way for the same seed', () => {
    const a = createWorld(readColors(), 7).particles.geometry.getAttribute('position').array;
    const b = createWorld(readColors(), 7).particles.geometry.getAttribute('position').array;
    expect(Array.from(a.slice(0, 30))).toEqual(Array.from(b.slice(0, 30)));
  });

  it('applies the effect flags', () => {
    const w = createWorld(readColors(), 7);
    expect(w.beams.children).toHaveLength(BEAM_COUNT);
    w.setEffects({ bloom: false, beams: false, particleShare: 0.25, cinematic: false });
    expect(w.beams.visible).toBe(false);
    expect(w.particles.geometry.drawRange.count).toBe(PARTICLE_COUNT / 4);
    w.setEffects({ bloom: true, beams: true, particleShare: 1, cinematic: true });
    expect(w.beams.visible).toBe(true);
    expect(w.particles.geometry.drawRange.count).toBe(PARTICLE_COUNT);
  });

  const mirrored = () => {
    const w = createWorld(readColors(), 7);
    const mat = new THREE.MeshBasicMaterial({ transparent: true, opacity: 1 });
    const src = new THREE.Mesh(new THREE.SphereGeometry(0.5), mat);
    src.position.y = 1.2;
    w.mirror(src);
    const copy = w.group.getObjectByName('mirror')!.children[0] as THREE.Mesh;
    return { w, src, mat, copy };
  };
  const opacity = (m: THREE.Mesh) => (m.material as THREE.Material).opacity;

  it('mirrors a source below the floor in world space and follows its rotation', () => {
    const { w, src, copy } = mirrored();
    src.rotation.y = 1;
    w.update(0.016, 1);
    // The flip happens once, on the mirror group, so the world-space height
    // is below the floor even though the local position is the source's.
    const world = copy.getWorldPosition(new THREE.Vector3());
    expect(world.y).toBeCloseTo(-1.2);
    expect(copy.position.y).toBeCloseTo(1.2);
    expect(copy.rotation.y).toBeCloseTo(1);
  });

  it('copies the source opacity each frame at a quarter strength', () => {
    const { w, mat, copy } = mirrored();
    w.update(0.016, 1);
    expect(opacity(copy)).toBeCloseTo(0.25);
    mat.opacity = 0.8;
    w.update(0.016, 2);
    expect(opacity(copy)).toBeCloseTo(0.2);
  });

  it('fades the reflection in with the boot reveal', () => {
    const { w, copy } = mirrored();
    w.setReveal(0);
    w.update(0.016, 1);
    expect(opacity(copy)).toBeCloseTo(0);
    w.setReveal(REVEAL_FULL / 2);
    w.update(0.016, 2);
    expect(opacity(copy)).toBeCloseTo(0.125);
    w.setReveal(REVEAL_FULL);
    w.update(0.016, 3);
    expect(opacity(copy)).toBeCloseTo(0.25);
  });
});

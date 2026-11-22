import { describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { BEAM_COUNT, createWorld, PARTICLE_COUNT, REVEAL_FULL, SKY_HEIGHT, skyMix } from './world';
import { readColors } from './colors';

describe('createWorld', () => {
  it('draws every particle in one Points object', () => {
    const w = createWorld(readColors(), 7);
    let points = 0;
    w.group.traverse((o) => { if ((o as THREE.Points).isPoints) points++; });
    expect(points).toBe(1);
    expect(w.particles.geometry.getAttribute('position').count).toBe(PARTICLE_COUNT);
  });

  it('draws the dust as soft round motes on the near side of the camera', () => {
    const w = createWorld(readColors(), 7);
    const material = w.particles.material as THREE.PointsMaterial;
    expect(material.map).toBeInstanceOf(THREE.CanvasTexture);
    // A size that grows with proximity turns a near mote into a square; a
    // fixed sprite size is the clamp.
    expect(material.sizeAttenuation).toBe(false);
    expect(material.size).toBeLessThanOrEqual(8);
    const position = w.particles.geometry.getAttribute('position');
    for (let i = 0; i < position.count; i++) {
      expect(position.getZ(i)).toBeLessThanOrEqual(5);
    }
  });

  it('moves and uploads only the particles in the active draw range', () => {
    const w = createWorld(readColors(), 7);
    const position = w.particles.geometry.getAttribute('position') as THREE.BufferAttribute;
    const array = position.array as Float32Array;
    w.setEffects({ bloom: true, beams: true, particleShare: 0.25, cinematic: true });
    const drawn = PARTICLE_COUNT / 4;
    const tail = Array.from(array.slice(drawn * 3));

    w.update(0.016, 1);
    expect(Array.from(array.slice(drawn * 3))).toEqual(tail);
    expect(array[1]).toBeGreaterThan(0);
    expect(position.updateRanges).toEqual([{ start: 0, count: drawn * 3 }]);
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

  it('disposes every geometry, material and texture it owns', () => {
    const w = createWorld(readColors(), 7);
    const geometry = vi.spyOn(w.floor.geometry, 'dispose');
    const material = vi.spyOn(w.floor.material as THREE.Material, 'dispose');
    const texture = vi.spyOn((w.particles.material as THREE.PointsMaterial).map!, 'dispose');

    w.dispose();
    expect(geometry).toHaveBeenCalled();
    expect(material).toHaveBeenCalled();
    expect(texture).toHaveBeenCalled();
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

describe('skyMix', () => {
  it('keeps haze at and below the horizon and reaches void by SKY_HEIGHT', () => {
    expect(skyMix(0)).toBe(0);
    expect(skyMix(-12)).toBe(0);
    expect(skyMix(SKY_HEIGHT / 2)).toBeCloseTo(0.5);
    expect(skyMix(SKY_HEIGHT)).toBe(1);
    expect(skyMix(60)).toBe(1);
  });

  it('is resolved by the backdrop: haze at the horizon, void at the zenith', () => {
    const colors = readColors();
    const w = createWorld(colors, 7);
    const backdrop = w.group.getObjectByName('backdrop') as THREE.Mesh;
    const position = backdrop.geometry.getAttribute('position');
    const color = backdrop.geometry.getAttribute('color');
    const ring = (y: number) => {
      for (let i = 0; i < position.count; i++) {
        if (Math.abs(position.getY(i) - y) < 1e-6) {
          return new THREE.Color(color.getX(i), color.getY(i), color.getZ(i));
        }
      }
      throw new Error(`the backdrop has no ring at y=${y}`);
    };
    expect(ring(0).getHex()).toBe(colors.haze.getHex());
    expect(ring(60).getHex()).toBe(colors.void.getHex());

    // The band the fade lives in needs rings of its own; one segment would
    // interpolate straight past it and leave the sky a flat purple.
    const ys = new Set<number>();
    for (let i = 0; i < position.count; i++) ys.add(Math.round(position.getY(i) * 100) / 100);
    const band = [...ys].filter((y) => y > 0 && y < SKY_HEIGHT);
    expect(band.length).toBeGreaterThanOrEqual(2);
  });
});

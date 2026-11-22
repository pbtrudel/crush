import { describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { coreLook, createCore, stepColor } from './core';
import { readColors } from './colors';

const cyan = new THREE.Color('#52d8ff');
const magenta = new THREE.Color('#ff4fd8');

// Moved here from constellation.test.ts, which Task 7 deletes.
describe('stepColor', () => {
  it('reaches the target in one step when motion is reduced or off', () => {
    for (const motion of ['reduced', 'off'] as const) {
      const c = cyan.clone();
      stepColor(c, magenta, motion);
      expect(c.equals(magenta)).toBe(true);
    }
  });

  it('moves part way toward the target in full motion', () => {
    const c = cyan.clone();
    stepColor(c, magenta, 'full');
    expect(c.equals(magenta)).toBe(false);
    expect(c.r).toBeCloseTo(cyan.r + (magenta.r - cyan.r) * 0.2, 6);
    expect(c.g).toBeCloseTo(cyan.g + (magenta.g - cyan.g) * 0.2, 6);
    expect(c.b).toBeCloseTo(cyan.b + (magenta.b - cyan.b) * 0.2, 6);
  });
});

describe('coreLook', () => {
  it('breathes slowly when idle and spins faster while working', () => {
    expect(coreLook('idle')).toMatchObject({ color: 'cyan', pulsePeriod: 4, flicker: false });
    expect(coreLook('working').spin).toBeGreaterThan(coreLook('idle').spin);
    expect(coreLook('working').intensity).toBeGreaterThan(coreLook('idle').intensity);
  });

  it('turns magenta when it needs you and flickers on error', () => {
    expect(coreLook('attention')).toMatchObject({ color: 'magenta', flicker: false });
    expect(coreLook('error')).toMatchObject({ color: 'magenta', flicker: true });
  });
});

describe('createCore', () => {
  const ring = (c: ReturnType<typeof createCore>) => c.group.getObjectByName('context-ring') as THREE.Mesh;

  it('disposes the geometries and materials in its group', () => {
    const c = createCore(readColors());
    const crystal = c.group.getObjectByName('crystal') as THREE.Mesh;
    const geometry = vi.spyOn(crystal.geometry, 'dispose');
    const material = vi.spyOn(crystal.material as THREE.Material, 'dispose');

    c.dispose();
    expect(geometry).toHaveBeenCalled();
    expect(material).toHaveBeenCalled();
  });

  it('stacks three projector rings under a light cone', () => {
    const c = createCore(readColors());
    expect(c.group.getObjectByName('projector')!.children.filter((o) => o.name === 'pedestal-ring')).toHaveLength(3);
    expect(c.group.getObjectByName('cone')).toBeDefined();
  });

  it('colors the context ring by level and sizes it by fill', () => {
    const colors = readColors();
    const c = createCore(colors);
    c.setFill(0.5);
    const mat = () => ring(c).material as THREE.MeshBasicMaterial;
    expect(mat().color.equals(colors.cyan)).toBe(true);
    c.setFill(0.8);
    expect(mat().color.equals(colors.amber)).toBe(true);
    c.setFill(0.95);
    expect(mat().color.equals(colors.magenta)).toBe(true);
    const len = (ring(c).geometry as THREE.RingGeometry).parameters.thetaLength;
    expect(len).toBeCloseTo(Math.PI * 2 * 0.95);
  });

  it('snaps to the attention color at once outside full motion', () => {
    const colors = readColors();
    const c = createCore(colors);
    c.setMode('attention');
    c.update(0.016, 0, 'reduced');
    const heart = c.group.getObjectByName('heart') as THREE.Mesh;
    expect((heart.material as THREE.MeshBasicMaterial).color.equals(colors.magenta)).toBe(true);
  });

  it('reports that it is animating while a flash fades', () => {
    const c = createCore(readColors());
    c.flash(readColors().teal);
    expect(c.update(0.016, 0, 'reduced')).toBe(true);
    for (let i = 0; i < 100; i++) c.update(0.016, i * 0.016, 'reduced');
    expect(c.update(0.016, 2, 'reduced')).toBe(false);
  });

  it('decays a flash in real time even when motion is off', () => {
    const c = createCore(readColors());
    c.flash(readColors().teal);
    expect(c.update(0.1, 0, 'off')).toBe(true);
    // Six real steps end the flash; the motion scale must not stretch it.
    for (let i = 0; i < 6; i++) c.update(0.1, 0.1 * (i + 1), 'off');
    expect(c.update(0.1, 1, 'off')).toBe(false);
  });

  it('scales the spin by the motion mode and nothing else', () => {
    const spun = (motion: 'full' | 'reduced' | 'off') => {
      const c = createCore(readColors());
      c.update(1, 0, motion);
      return c.emissive.rotation.y;
    };
    const full = spun('full');
    expect(full).toBeCloseTo(coreLook('idle').spin);
    // The step is the real one, so reduced is a quarter, not a sixteenth.
    expect(spun('reduced')).toBeCloseTo(full * 0.25);
    expect(spun('off')).toBe(0);
  });

  it('settles a full motion color change in bounded time', () => {
    const colors = readColors();
    const c = createCore(colors);
    c.setMode('attention');
    let animating = true;
    let steps = 0;
    for (; steps < 600 && animating; steps++) {
      animating = c.update(0.016, steps * 0.016, 'full');
    }
    expect(steps).toBeLessThan(600);
    expect(animating).toBe(false);
    const heart = c.group.getObjectByName('heart') as THREE.Mesh;
    expect((heart.material as THREE.MeshBasicMaterial).color.equals(colors.magenta)).toBe(true);
  });
});

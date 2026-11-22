import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { stepColor } from './core';

const cyan = new THREE.Color('#52d8ff');
const magenta = new THREE.Color('#ff4fd8');

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

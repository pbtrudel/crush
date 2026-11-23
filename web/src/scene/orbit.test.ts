import { describe, expect, it } from 'vitest';
import { ORBIT, orbit2D, orbitAngle, orbitPoint } from './orbit';

describe('orbit', () => {
  it('spaces nodes evenly and drifts with time', () => {
    expect(orbitAngle(1, 4, 0, ORBIT.speed) - orbitAngle(0, 4, 0, ORBIT.speed)).toBeCloseTo(Math.PI / 2);
    expect(orbitAngle(0, 4, 10, ORBIT.speed)).toBeCloseTo(orbitAngle(0, 4, 0, ORBIT.speed) + 10 * ORBIT.speed);
  });

  it('keeps every point on the tilted ring around the core', () => {
    for (const a of [0, 1, 2, 3, 4, 5]) {
      const p = orbitPoint(a);
      expect(Math.hypot(p.x, p.y - ORBIT.height, p.z)).toBeCloseTo(ORBIT.radius);
      expect(Math.abs(p.y - ORBIT.height)).toBeLessThanOrEqual(ORBIT.radius * Math.sin(ORBIT.tilt) + 1e-9);
    }
  });

  it('places 2D nodes on the outer ring ellipse', () => {
    const p = orbit2D(0, 3);
    expect(((p.x / 150) ** 2) + (((p.y - 70) / 34) ** 2)).toBeCloseTo(1);
  });
});

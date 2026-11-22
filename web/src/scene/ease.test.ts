import { describe, expect, it } from 'vitest';
import { expoOut } from './ease';

describe('expoOut', () => {
  it('is exactly 0 at and before the start and exactly 1 at and after the end', () => {
    expect(expoOut(0)).toBe(0);
    expect(expoOut(-3)).toBe(0);
    expect(expoOut(1)).toBe(1);
    expect(expoOut(7)).toBe(1);
  });

  it('rises quickly and monotonically in between', () => {
    expect(expoOut(0.1)).toBeCloseTo(1 - 2 ** -1);
    expect(expoOut(0.5)).toBeCloseTo(1 - 2 ** -5);
    let last = 0;
    for (let t = 0.05; t < 1; t += 0.05) {
      const v = expoOut(t);
      expect(v).toBeGreaterThan(last);
      last = v;
    }
    expect(last).toBeLessThan(1);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { mulberry32, sceneSeed } from './rng';

describe('mulberry32', () => {
  it('repeats for the same seed and stays in [0, 1)', () => {
    const a = mulberry32(42), b = mulberry32(42);
    for (let i = 0; i < 100; i++) {
      const x = a();
      expect(x).toBe(b());
      expect(x).toBeGreaterThanOrEqual(0);
      expect(x).toBeLessThan(1);
    }
  });

  it('differs across seeds', () => {
    expect(mulberry32(1)()).not.toBe(mulberry32(2)());
  });
});

describe('sceneSeed', () => {
  afterEach(() => {
    localStorage.removeItem('crush-web-seed');
    vi.useRealTimers();
  });

  it('uses the stored seed when it is an integer', () => {
    localStorage.setItem('crush-web-seed', '99');
    expect(sceneSeed()).toBe(99);
  });

  it('ignores a blank stored seed', () => {
    localStorage.setItem('crush-web-seed', '  ');
    try {
      expect(sceneSeed()).not.toBe(0);
    } finally {
      localStorage.removeItem('crush-web-seed');
    }
  });

  it('falls back to the clock when the stored value is not an integer', () => {
    vi.useFakeTimers();
    vi.setSystemTime(1234);
    localStorage.setItem('crush-web-seed', '12.5');
    expect(sceneSeed()).toBe(1234);
    localStorage.setItem('crush-web-seed', 'later');
    expect(sceneSeed()).toBe(1234);
  });
});

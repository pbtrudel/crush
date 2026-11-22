import { describe, expect, it } from 'vitest';
import { BOOT_MS, bootFrame } from './boot';

describe('bootFrame', () => {
  it('draws the grid outward, then stacks the rings, then ignites the core', () => {
    const start = bootFrame(0, 'full');
    expect(start).toEqual({ reveal: 0, rings: 0, ignite: 0, done: false });
    const mid = bootFrame(BOOT_MS * 0.5, 'full');
    expect(mid.reveal).toBeGreaterThan(0);
    expect(mid.rings).toBeGreaterThan(0);
    expect(mid.ignite).toBe(0);
    expect(bootFrame(BOOT_MS, 'full')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
  });

  it('skips straight to the end outside full motion', () => {
    expect(bootFrame(0, 'reduced')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
    expect(bootFrame(0, 'off')).toEqual({ reveal: 40, rings: 3, ignite: 1, done: true });
  });
});

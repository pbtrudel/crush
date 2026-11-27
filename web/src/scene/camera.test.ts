import { describe, expect, it } from 'vitest';
import { parallax, viewOffset } from './camera';

describe('viewOffset', () => {
  it('is zero without a focus rect', () => {
    expect(viewOffset({ width: 1440, height: 860 }, null)).toEqual({ x: 0, y: 0 });
  });

  it('shifts the view so the projector sits in the middle of the focus rect', () => {
    // Focus centered at x = 1000: the image must move right by 280 px, which
    // is a negative view offset in three.js terms.
    expect(viewOffset({ width: 1440, height: 860 }, { x: 600, y: 100, width: 800, height: 600 })).toEqual({ x: -280, y: -(400 - 430) });
  });
});

describe('parallax', () => {
  it('follows the pointer by at most 2 degrees in full motion', () => {
    const max = (2 * Math.PI) / 180;
    expect(parallax({ x: 1, y: -1 }, 'full')).toEqual({ yaw: max, pitch: -max });
    expect(parallax({ x: 5, y: 0 }, 'full').yaw).toBeCloseTo(max);
  });

  it('treats a non-finite pointer as centered', () => {
    expect(parallax({ x: Number.NaN, y: Number.POSITIVE_INFINITY }, 'full')).toEqual({ yaw: 0, pitch: 0 });
  });

  it('is still in reduced and off motion', () => {
    expect(parallax({ x: 1, y: 1 }, 'reduced')).toEqual({ yaw: 0, pitch: 0 });
    expect(parallax({ x: 1, y: 1 }, 'off')).toEqual({ yaw: 0, pitch: 0 });
  });
});

import { describe, expect, it } from 'vitest';
import { clampStep, frameInterval, MAX_STEP_S } from './loop';

describe('frameInterval', () => {
  it('runs at 60 fps for events, 30 fps for ambient motion, and stops when hidden or off', () => {
    expect(frameInterval({ hidden: false, motion: 'full', eventActive: true })).toBeCloseTo(1000 / 60);
    expect(frameInterval({ hidden: false, motion: 'full', eventActive: false })).toBeCloseTo(1000 / 30);
    expect(frameInterval({ hidden: false, motion: 'reduced', eventActive: false })).toBeCloseTo(1000 / 30);
    expect(frameInterval({ hidden: true, motion: 'full', eventActive: true })).toBeNull();
    expect(frameInterval({ hidden: false, motion: 'off', eventActive: true })).toBeNull();
  });
});

describe('clampStep', () => {
  it('clamps the step after a long gap', () => {
    expect(clampStep(16)).toBeCloseTo(0.016);
    expect(clampStep(120_000)).toBe(MAX_STEP_S);
    expect(clampStep(-5)).toBe(0);
  });
});

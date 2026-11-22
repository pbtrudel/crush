import { describe, expect, it } from 'vitest';
import { motionScale } from './motionScale';

describe('motionScale', () => {
  it('runs ambient motion at a quarter speed when reduced and stops it when off', () => {
    expect(motionScale('full')).toBe(1);
    expect(motionScale('reduced')).toBe(0.25);
    expect(motionScale('off')).toBe(0);
  });
});

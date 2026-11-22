import { describe, expect, it, vi } from 'vitest';
import { lowPower } from './motion';
import {
  effectFlags,
  effectsLevel,
  resetWebglProbe,
  restoreEffects,
  sceneFailed,
  webglSupported,
} from './effects';

describe('sceneFailed', () => {
  it('resets when low power turns off, and only then', () => {
    lowPower.value = false;
    sceneFailed.value = true;
    // A failure with low power already off stays failed.
    expect(sceneFailed.value).toBe(true);
    lowPower.value = true;
    expect(sceneFailed.value).toBe(true);
    lowPower.value = false;
    expect(sceneFailed.value).toBe(false);
  });

  it('is cleared by restoreEffects, which also resets the level', () => {
    effectsLevel.value = 2;
    sceneFailed.value = true;
    restoreEffects();
    expect(effectsLevel.value).toBe(0);
    expect(sceneFailed.value).toBe(false);
  });

  it('retries the WebGL probe that a failed scene left cached', () => {
    resetWebglProbe();
    const getContext = vi.spyOn(HTMLCanvasElement.prototype, 'getContext');
    getContext.mockReturnValue(null);
    expect(webglSupported()).toBe(false);
    // The probe is cached, so a newly available context changes nothing.
    getContext.mockReturnValue({ getExtension: () => null } as unknown as WebGL2RenderingContext);
    expect(webglSupported()).toBe(false);
    restoreEffects();
    expect(webglSupported()).toBe(true);
    getContext.mockRestore();
    resetWebglProbe();
  });
});

describe('effectFlags', () => {
  it('steps down bloom, then beams, then half the particles', () => {
    expect(effectFlags(0, false)).toEqual({ bloom: true, beams: true, particleShare: 1, cinematic: true });
    expect(effectFlags(1, false)).toEqual({ bloom: false, beams: true, particleShare: 1, cinematic: true });
    expect(effectFlags(2, false)).toEqual({ bloom: false, beams: false, particleShare: 1, cinematic: true });
    expect(effectFlags(3, false)).toEqual({ bloom: false, beams: false, particleShare: 0.5, cinematic: true });
  });

  it('low power wins over any level', () => {
    for (const level of [0, 1, 2, 3] as const) {
      expect(effectFlags(level, true)).toEqual({ bloom: false, beams: false, particleShare: 0.25, cinematic: false });
    }
  });
});

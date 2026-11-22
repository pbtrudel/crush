import { effect, signal } from '@preact/signals';
import { lowPower } from './motion';

// How far the scene has stepped down from full effects; later tasks raise it
// from the frame-time guard and the restore toast lowers it back to 0.
export type EffectsLevel = 0 | 1 | 2 | 3;

export const effectsLevel = signal<EffectsLevel>(0);
export const sceneFailed = signal(false);

// The probe is cached: every probe creates a WebGL context, and browsers cap
// how many may exist at the same time.
let probe: boolean | null = null;

export function webglSupported(): boolean {
  if (probe !== null) return probe;
  try {
    const gl = document.createElement('canvas').getContext('webgl2');
    // Release the probe context before dropping the reference.
    gl?.getExtension('WEBGL_lose_context')?.loseContext();
    probe = !!gl;
  } catch {
    probe = false;
  }
  return probe;
}

// Test-only: clears the cached probe so a test can exercise either branch.
export function resetWebglProbe(): void {
  probe = null;
}

export function use3D(): boolean {
  return webglSupported() && !lowPower.value && !sceneFailed.value;
}

export interface EffectFlags { bloom: boolean; beams: boolean; particleShare: number; cinematic: boolean }

export function effectFlags(level: EffectsLevel, lowPower: boolean): EffectFlags {
  if (lowPower) return { bloom: false, beams: false, particleShare: 0.25, cinematic: false };
  return {
    bloom: level < 1,
    beams: level < 2,
    particleShare: level >= 3 ? 0.5 : 1,
    cinematic: true,
  };
}

// Restore is the way back from a failed scene, so it also forgets the
// cached probe: the next mount has to ask the browser for a context again.
export function restoreEffects(): void {
  effectsLevel.value = 0;
  sceneFailed.value = false;
  resetWebglProbe();
}

// Leaving low power must retry the scene its own toggle disposed. Watching
// for the true to false edge keeps a failure that happened while low power
// was off from being cleared by anything but the toggle or the restore.
let wasLowPower = lowPower.value;
effect(() => {
  const now = lowPower.value;
  if (wasLowPower && !now) sceneFailed.value = false;
  wasLowPower = now;
});

import type { Motion } from '../state/motion';

// Ambient motion runs at a quarter speed when reduced and stops when off.
// Callers pass the real step, so the scale is applied once, here.
export function motionScale(m: Motion): number {
  if (m === 'full') return 1;
  if (m === 'reduced') return 0.25;
  return 0;
}

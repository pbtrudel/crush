import type { Motion } from '../state/motion';

// Events animate at 60 fps; ambient drift is fine at 30. A hidden tab or a
// motion setting of off stops the chain instead of slowing it.
const EVENT_MS = 1000 / 60;
const AMBIENT_MS = 1000 / 30;

export function frameInterval(o: { hidden: boolean; motion: Motion; eventActive: boolean }): number | null {
  if (o.hidden || o.motion === 'off') return null;
  return o.eventActive ? EVENT_MS : AMBIENT_MS;
}

// A tab that was hidden for minutes returns one enormous step, which would
// teleport the animation; the clamp keeps it to a plausible frame.
export const MAX_STEP_S = 0.05;

export function clampStep(dtMs: number): number {
  return Math.min(MAX_STEP_S, Math.max(0, dtMs / 1000));
}

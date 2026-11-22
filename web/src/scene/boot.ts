import type { Motion } from '../state/motion';
import { expoOut } from './ease';
import { REVEAL_FULL } from './world';

// The whole power-up: the grid draws outward, then the pedestal rings stack,
// then the core ignites.
export const BOOT_MS = 1400;

// Phase boundaries as a fraction of BOOT_MS. The grid finishes before the
// rings finish stacking, and the ignition overlaps neither.
const REVEAL_END = 0.45;
const RINGS_START = 0.35;
const RINGS_END = 0.7;
const IGNITE_START = 0.7;

const RINGS = 3;

// The ignition is the boot's last phase; the scene holds it until the
// session links instead of lighting the core before the overlay goes.
export const BOOT_IGNITE_MS = BOOT_MS * IGNITE_START;

export interface BootFrame {
  reveal: number;
  rings: number;
  ignite: number;
  done: boolean;
}

const clamp01 = (n: number) => Math.min(1, Math.max(0, n));

// phase maps the elapsed fraction onto 0 to 1 inside one window.
const phase = (t: number, start: number, end: number) => clamp01((t - start) / (end - start));

export function bootFrame(elapsedMs: number, motion: Motion): BootFrame {
  // Reduced and off motion have no power-up to watch, so the scene opens
  // already lit.
  if (motion !== 'full') return { reveal: REVEAL_FULL, rings: RINGS, ignite: 1, done: true };
  const t = clamp01(elapsedMs / BOOT_MS);
  return {
    reveal: REVEAL_FULL * expoOut(phase(t, 0, REVEAL_END)),
    // Rings appear one at a time as the window opens.
    rings: Math.min(RINGS, Math.ceil(phase(t, RINGS_START, RINGS_END) * RINGS)),
    ignite: phase(t, IGNITE_START, 1),
    done: t >= 1,
  };
}

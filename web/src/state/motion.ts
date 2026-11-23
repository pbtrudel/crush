import { signal } from '@preact/signals';
import { readStored, writeStored } from './storage';

export type Motion = 'full' | 'reduced' | 'off';
const KEY_MOTION = 'crush-web-motion';
const KEY_LOW = 'crush-web-low-power';

function initialMotion(): Motion {
  const saved = readStored(KEY_MOTION) as Motion | null;
  if (saved === 'full' || saved === 'reduced' || saved === 'off') return saved;
  const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  return reduce ? 'reduced' : 'full';
}

export const motion = signal<Motion>(initialMotion());
export const lowPower = signal<boolean>(readStored(KEY_LOW) === '1');

export function setMotion(m: Motion) {
  motion.value = m;
  writeStored(KEY_MOTION, m);
}

export function setLowPower(on: boolean) {
  lowPower.value = on;
  writeStored(KEY_LOW, on ? '1' : '0');
}

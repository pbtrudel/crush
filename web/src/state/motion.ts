import { signal } from '@preact/signals';

export type Motion = 'full' | 'reduced' | 'off';
const KEY_MOTION = 'crush-web-motion';
const KEY_LOW = 'crush-web-low-power';

function initialMotion(): Motion {
  const saved = localStorage.getItem(KEY_MOTION) as Motion | null;
  if (saved === 'full' || saved === 'reduced' || saved === 'off') return saved;
  const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  return reduce ? 'reduced' : 'full';
}

export const motion = signal<Motion>(initialMotion());
export const lowPower = signal<boolean>(localStorage.getItem(KEY_LOW) === '1');

export function setMotion(m: Motion) {
  motion.value = m;
  localStorage.setItem(KEY_MOTION, m);
}

export function setLowPower(on: boolean) {
  lowPower.value = on;
  localStorage.setItem(KEY_LOW, on ? '1' : '0');
}

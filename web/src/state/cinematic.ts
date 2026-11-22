import { computed, effect, signal } from '@preact/signals';
import { effectFlags, effectsLevel, use3D } from './effects';
import { lowPower } from './motion';

// Cinematic mode is a view state, not a preference: a reload starts in the
// normal layout.
export const cinematic = signal(false);

// The flight and the panel cross-fade, in milliseconds. They live with the
// mode's state so eager code can read them without the lazy scene chunk.
export const FLIGHT_MS = 900;
export const FADE_MS = 150;

// The one place the rule for entering cinematic mode is written. The App
// reads it for the key, the HUD for its button and the scene layer for the
// flight; none of them repeats the expression.
const allowed = computed(() => use3D() && effectFlags(effectsLevel.value, lowPower.value).cinematic);

export function cinematicAllowed(): boolean {
  return allowed.value;
}

// Entering fails closed: without a live 3D scene there is nothing to fly.
export function setCinematic(on: boolean): void {
  cinematic.value = on && allowed.value;
}

export type KeyContext = {
  code: string;
  altKey: boolean;
  inPrompt: boolean;
  dialogOpen: boolean;
  active: boolean;
  allowed: boolean;
};

// The shortcut is matched on `code`, not `key`: with Alt held macOS reports
// the composed character ('ç') instead of 'c'.
export function cinematicKey(k: KeyContext): 'toggle' | 'exit' | null {
  if (k.code === 'KeyC' && k.altKey) return k.allowed ? 'toggle' : null;
  // In the prompt Esc keeps cancelling a run, and an open dialog owns Esc.
  if (k.code === 'Escape' && k.active && !k.inPrompt && !k.dialogOpen) return 'exit';
  return null;
}

// A scene that fails, or a display that steps down to low power, takes the
// mode down with it.
effect(() => {
  if (cinematic.value && !allowed.value) cinematic.value = false;
});

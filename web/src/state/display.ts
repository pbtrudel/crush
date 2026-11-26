import { effect, signal } from '@preact/signals';
import { api } from '../api/http';
import type { DisplaySettings } from '../api/types';
import { describeFailure, type Failure } from './failure';
import { KEY_MOTION, motion, setLowPower, setMotion, type Motion } from './motion';
import { closePanels } from './palette';
import { displayDefaults, type State } from './reducer';
import { removeStored } from './storage';
import { dispatch, state } from './store';

// displayOpen holds the HUD's display settings dialog, and displayConfirmStop
// opens it at the Stop web mode confirm step.
export const displayOpen = signal(false);
export const displayConfirmStop = signal(false);

// openDisplay closes every panel first, so two dialogs never stack.
export function openDisplay(confirmStop = false): void {
  closePanels();
  displayConfirmStop.value = confirmStop;
  displayOpen.value = true;
}

export function closeDisplay(): void {
  displayOpen.value = false;
  displayConfirmStop.value = false;
}

// escapeDisplay handles Escape while the dialog is open and reports whether
// it consumed the press, so the inspector and the cinematic view never see
// the same key.
export function escapeDisplay(): boolean {
  if (!displayOpen.value) return false;
  closeDisplay();
  return true;
}

// effectiveMotion is the one place the config value and the browser's
// reduced motion setting meet: the config value wins, then the browser's
// setting.
export function effectiveMotion(d: DisplaySettings, prefersReduced: boolean): Motion {
  if (d.motion !== '') return d.motion;
  return prefersReduced ? 'reduced' : 'full';
}

function prefersReduced(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// pushDisplay moves the config into the signals the page renders from. It
// waits for the snapshot and for a display the gateway named: until then the
// tab renders from its own cache, and the config's defaults must not put a
// low-power tab back on the 3D scene.
let applied: DisplaySettings | null = null;
effect(() => {
  const s = state.value;
  if (!s.hydrated || !s.displayHydrated) return;
  const d = s.display;
  if (d === applied) return;
  applied = d;
  if (d.motion === '') {
    // Follow the system forgets the tab's choice, so the cache goes and the
    // browser's reduced-motion setting decides. No setter here: setMotion
    // would write the cache back.
    removeStored(KEY_MOTION);
    motion.value = effectiveMotion(d, prefersReduced());
  } else {
    setMotion(d.motion);
  }
  setLowPower(d.low_power);
});

const DISPLAY_KEYS: (keyof DisplaySettings)[] = ['theme', 'motion', 'low_power', 'cinematic_hint'];
const KEY_PREFIX = 'options.web_display.';

// withKey returns a state whose display holds one new value. The generic
// signature keeps the value tied to its key.
function withKey<K extends keyof DisplaySettings>(s: State, key: K, value: DisplaySettings[K]): State {
  const display: DisplaySettings = { ...s.display };
  display[key] = value;
  return { ...s, display, displayHydrated: true };
}

// writes is the one chain every config write from this dialog runs through,
// so two requests never overlap and the gateway sees them in order.
let writes: Promise<unknown> = Promise.resolve();

function enqueue<T>(work: () => Promise<T>): Promise<T> {
  const run = writes.then(work, work);
  writes = run.then(() => undefined, () => undefined);
  return run;
}

export const actions = {
  // setDisplay shows the new value at once and writes it, then puts it back
  // when the gateway refuses. The empty motion is the absence of the key,
  // so it removes rather than writes it.
  async setDisplay<K extends keyof DisplaySettings>(key: K, value: DisplaySettings[K]): Promise<Failure | null> {
    const prev = state.value.display[key];
    dispatch((s) => withKey(s, key, value));
    return enqueue(async () => {
      try {
        if (value === '') await api.removeConfig(KEY_PREFIX + key);
        else await api.setConfig(KEY_PREFIX + key, value);
        return null;
      } catch (err) {
        // Only a control still showing the refused value goes back: a newer
        // choice for the same key is a write of its own, still queued.
        dispatch((s) => (s.display[key] === value ? withKey(s, key, prev) : s));
        return describeFailure(err);
      }
    });
  },

  // resetDisplay removes every key, so each falls back to its default. A
  // failed removal puts only its own key back and leaves the rest removed.
  async resetDisplay(): Promise<Failure | null> {
    const prev = state.value.display;
    dispatch((s) => ({ ...s, display: { ...displayDefaults }, displayHydrated: true }));
    return enqueue(async () => {
      let failure: Failure | null = null;
      for (const key of DISPLAY_KEYS) {
        try {
          await api.removeConfig(KEY_PREFIX + key);
        } catch (err) {
          dispatch((s) => withKey(s, key, prev[key]));
          failure ??= describeFailure(err);
        }
      }
      return failure;
    });
  },
};

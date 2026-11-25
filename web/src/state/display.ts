import { effect, signal } from '@preact/signals';
import { api } from '../api/http';
import type { DisplaySettings } from '../api/types';
import { describeFailure, type Failure } from './failure';
import { KEY_MOTION, setLowPower, setMotion, type Motion } from './motion';
import { closePanels } from './palette';
import { displayDefaults, type State } from './reducer';
import { readStored } from './storage';
import { dispatch, state } from './store';

// displayOpen holds the HUD's display settings dialog.
export const displayOpen = signal(false);

// openDisplay closes every panel first, so two dialogs never stack.
export function openDisplay(): void {
  closePanels();
  displayOpen.value = true;
}

export function closeDisplay(): void {
  displayOpen.value = false;
}

// escapeDisplay handles Escape while the dialog is open and reports whether
// it consumed the press, so the inspector and the cinematic view never see
// the same key.
export function escapeDisplay(): boolean {
  if (!displayOpen.value) return false;
  closeDisplay();
  return true;
}

// effectiveMotion is the one place the three motion sources meet: the config
// value wins, then the tab's remembered motion, then the browser's reduced
// motion setting.
export function effectiveMotion(d: DisplaySettings, stored: Motion | null, prefersReduced: boolean): Motion {
  if (d.motion !== '') return d.motion;
  if (stored !== null) return stored;
  return prefersReduced ? 'reduced' : 'full';
}

// storedMotion is the per-tab cache motion.ts keeps, or null when the tab
// never chose one.
function storedMotion(): Motion | null {
  const saved = readStored(KEY_MOTION);
  return saved === 'full' || saved === 'reduced' || saved === 'off' ? saved : null;
}

function prefersReduced(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// pushDisplay moves the config into the signals the page renders from. The
// applied object is remembered so an unrelated state change writes neither
// signal nor storage.
let applied: DisplaySettings | null = null;
effect(() => {
  const d = state.value.display;
  if (d === applied) return;
  applied = d;
  setMotion(effectiveMotion(d, storedMotion(), prefersReduced()));
  setLowPower(d.low_power);
});

const DISPLAY_KEYS: (keyof DisplaySettings)[] = ['theme', 'motion', 'low_power', 'cinematic_hint'];
const KEY_PREFIX = 'options.web_display.';

// withKey returns a state whose display holds one new value. The generic
// signature keeps the value tied to its key.
function withKey<K extends keyof DisplaySettings>(s: State, key: K, value: DisplaySettings[K]): State {
  const display: DisplaySettings = { ...s.display };
  display[key] = value;
  return { ...s, display };
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
    dispatch((s) => ({ ...s, display: { ...displayDefaults } }));
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

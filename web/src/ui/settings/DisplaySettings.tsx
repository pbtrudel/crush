import { useEffect, useRef, useState } from 'preact/hooks';
import { api } from '../../api/http';
import type { DisplaySettings as Display, Theme } from '../../api/types';
import { actions, closeDisplay, displayConfirmStop, displayOpen } from '../../state/display';
import { effectsLevel, restoreEffects, sceneFailed } from '../../state/effects';
import { describeFailure, type Failure } from '../../state/failure';
import { state } from '../../state/store';
import { useDialogFocus, usePaletteDeckFocus } from '../interrupt/focus';

const THEMES: { value: Theme; label: string }[] = [
  { value: 'holodeck', label: 'Holodeck' },
  { value: 'solid', label: 'Solid glass' },
];

const MOTIONS: { value: Display['motion']; label: string }[] = [
  { value: '', label: 'Follow the system' },
  { value: 'full', label: 'Full' },
  { value: 'reduced', label: 'Reduced' },
  { value: 'off', label: 'Off' },
];

// DisplaySettings is the HUD's display dialog: the theme, the motion, the
// effects and the orbit hint, and the way to stop web mode. It is plain
// glass with cyan edges (the user opened it) and writes every change to the
// gateway's config through the shared actions.
export function DisplaySettings({ inert }: { inert: boolean }) {
  if (!displayOpen.value) return null;
  return <DisplayPanel inert={inert} />;
}

function DisplayPanel({ inert }: { inert: boolean }) {
  const d = state.value.display;
  const [failure, setFailure] = useState<Failure | null>(null);
  const [confirming, setConfirming] = useState(displayConfirmStop.value);
  const dialog = useRef<HTMLDivElement>(null);
  const stop = useRef<HTMLButtonElement>(null);
  const confirmStop = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);

  useDialogFocus(dialog, 'display', inert);
  usePaletteDeckFocus();

  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // The confirm step takes the focus; Cancel hands it back to the control
  // that opened the step.
  useEffect(() => {
    if (confirming) confirmStop.current?.focus();
  }, [confirming]);

  async function write<K extends keyof Display>(key: K, value: Display[K]): Promise<void> {
    const refused = await actions.setDisplay(key, value);
    if (alive.current) setFailure(refused);
  }

  const reset = async () => {
    const refused = await actions.resetDisplay();
    if (alive.current) setFailure(refused);
  };

  const cancelStop = () => {
    setConfirming(false);
    stop.current?.focus();
  };

  const detach = async () => {
    try {
      await api.detach();
      // The gateway is going away, and the ended page behind the dialog is
      // what explains it from here on.
      closeDisplay();
    } catch (err) {
      if (alive.current) setFailure(describeFailure(err));
    }
  };

  return (
    <div class="settings-layer" inert={inert}>
      <div
        class="settings glass scanlines enter"
        role="dialog"
        aria-modal="true"
        aria-label="Display settings"
        ref={dialog}
        tabIndex={-1}
      >
        <header class="settings__head">
          <h2 class="settings__title">Display settings</h2>
          <p class="settings__note">These settings live in the global config and follow you across runs.</p>
        </header>

        <fieldset class="settings__group">
          <legend class="hud-label">Theme</legend>
          {THEMES.map((t) => (
            <label key={t.value} class="settings__choice">
              <input
                type="radio"
                name="display-theme"
                value={t.value}
                checked={d.theme === t.value}
                onChange={() => void write('theme', t.value)}
              />
              {t.label}
            </label>
          ))}
        </fieldset>

        <fieldset class="settings__group">
          <legend class="hud-label">Motion</legend>
          {MOTIONS.map((m) => (
            <label key={m.value} class="settings__choice">
              <input
                type="radio"
                name="display-motion"
                value={m.value}
                checked={d.motion === m.value}
                onChange={() => void write('motion', m.value)}
              />
              {m.label}
            </label>
          ))}
        </fieldset>

        <fieldset class="settings__group">
          <legend class="hud-label">Effects</legend>
          <label class="settings__choice">
            <input
              type="checkbox"
              name="display-low-power"
              checked={d.low_power}
              onChange={(e) => void write('low_power', e.currentTarget.checked)}
            />
            Low power (2D constellation)
          </label>
          {/* The step-down toast dismisses itself, so the dialog is the
              lasting way back from reduced effects and from a failed scene. */}
          {(effectsLevel.value > 0 || sceneFailed.value) && (
            <button type="button" class="btn btn--ghost" onClick={restoreEffects}>Restore effects</button>
          )}
        </fieldset>

        <fieldset class="settings__group">
          <legend class="hud-label">Cinematic view</legend>
          <label class="settings__choice">
            <input
              type="checkbox"
              name="display-hint"
              checked={d.cinematic_hint}
              onChange={(e) => void write('cinematic_hint', e.currentTarget.checked)}
            />
            Show the orbit hint in cinematic view
          </label>
        </fieldset>

        <div class="settings__actions">
          <button type="button" class="btn btn--ghost" onClick={() => void reset()}>
            Reset display settings
          </button>
          <button type="button" class="btn btn--ghost" onClick={() => closeDisplay()}>Close</button>
        </div>

        <section class="settings__stop" aria-label="Stop web mode">
          <h3 class="hud-label">Stop web mode</h3>
          <p class="settings__note">The terminal keeps running. Run /web there for a new link.</p>
          <button ref={stop} type="button" class="btn" onClick={() => setConfirming(true)}>Stop web mode</button>
          {confirming && (
            <div class="settings__confirm" role="group" aria-label="Confirm stopping web mode">
              <p class="settings__note">Stop web mode? This tab disconnects.</p>
              <button ref={confirmStop} type="button" class="btn btn--danger" onClick={() => void detach()}>
                Stop web mode now
              </button>
              <button type="button" class="btn btn--ghost" onClick={cancelStop}>Cancel</button>
            </div>
          )}
        </section>

        {failure !== null && <p class="settings__error" role="alert">{failure.text}</p>}
      </div>
    </div>
  );
}

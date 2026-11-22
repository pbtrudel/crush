import { effectsLevel, restoreEffects, sceneFailed } from '../state/effects';
import { lowPower, motion, setLowPower, setMotion, type Motion } from '../state/motion';

export function MotionMenu() {
  return (
    <details class="motion-menu">
      <summary class="hud-label">Display</summary>
      <div class="motion-menu__panel">
      <fieldset>
        <legend class="hud-label">Motion</legend>
        {(['full', 'reduced', 'off'] as Motion[]).map((m) => (
          <label key={m}>
            <input type="radio" name="motion" value={m} checked={motion.value === m} onChange={() => setMotion(m)} />
            {m === 'full' ? 'Full' : m === 'reduced' ? 'Reduced' : 'Off'}
          </label>
        ))}
      </fieldset>
      <label>
        <input type="checkbox" checked={lowPower.value} onChange={(e) => setLowPower((e.currentTarget as HTMLInputElement).checked)} />
        Low power (2D constellation)
      </label>
      {/* The step-down toast dismisses itself, so the menu is the lasting
          way back from reduced effects and from a failed scene. */}
      {(effectsLevel.value > 0 || sceneFailed.value) && (
        <button type="button" class="btn btn--ghost" onClick={() => restoreEffects()}>Restore effects</button>
      )}
      </div>
    </details>
  );
}

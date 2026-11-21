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
      </div>
    </details>
  );
}

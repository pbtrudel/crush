import { useEffect, useRef } from 'preact/hooks';
import { effectsLevel, restoreEffects, sceneFailed } from '../state/effects';
import { lowPower, motion, setLowPower, setMotion, type Motion } from '../state/motion';

export function MotionMenu() {
  const summary = useRef<HTMLElement>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  const restore = () => {
    restoreEffects();
    // The button unmounts with the recovery, so focus moves to the menu
    // header instead of dropping to the body.
    summary.current?.focus();
  };

  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      const el = menu.current;
      if (el?.open && !el.contains(e.target as Node)) el.open = false;
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, []);

  // The menu owns its Escape: it closes itself, returns focus to the summary
  // and stops the press there, so the inspector, the cinematic view and the
  // question form keep their state.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || !menu.current?.open) return;
    e.preventDefault();
    e.stopPropagation();
    menu.current.open = false;
    summary.current?.focus();
  };

  return (
    <details ref={menu} class="motion-menu" onKeyDown={onKeyDown}>
      {/* tabIndex keeps the summary programmatically focusable after the
          restore button is gone. */}
      <summary ref={summary} tabIndex={0} class="hud-label">Display</summary>
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
        <button type="button" class="btn btn--ghost" onClick={restore}>Restore effects</button>
      )}
      </div>
    </details>
  );
}

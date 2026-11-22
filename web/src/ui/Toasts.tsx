import { useEffect } from 'preact/hooks';
import { actions, state } from '../state/store';
import { restoreEffects } from '../state/effects';

// Spec 3.5: toasts dismiss themselves after 6 s; an error stays until the
// reader dismisses it.
const TOAST_MS = 6000;

export function Toasts() {
  const toasts = state.value.toasts;
  useEffect(() => {
    const timers = toasts
      .filter((t) => t.tone !== 'error')
      .map((t) => window.setTimeout(() => actions.dismissToast(t.id), TOAST_MS));
    return () => timers.forEach((id) => window.clearTimeout(id));
  }, [toasts]);
  return (
    <ol class="toasts" aria-live="polite">
      {toasts.map((t) => (
        <li key={t.id} class="toast holo flicker-once" data-tone={t.tone}>
          <span class="toast__glyph" aria-hidden="true">{t.tone === 'error' ? '✕' : t.tone === 'warn' ? '!' : '◈'}</span>
          <span>{t.text}</span>
          {t.action === 'restore-effects' && (
            <button
              type="button"
              class="btn btn--ghost"
              onClick={() => {
                restoreEffects();
                actions.dismissToast(t.id);
              }}
            >Restore effects</button>
          )}
          <button type="button" class="toast__close" aria-label="Dismiss notification" onClick={() => actions.dismissToast(t.id)}>×</button>
        </li>
      ))}
    </ol>
  );
}

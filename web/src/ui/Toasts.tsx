import { useEffect } from 'preact/hooks';
import { actions, state } from '../state/store';
import { restoreEffects } from '../state/effects';
import { openSignIn } from '../state/signin';

// Spec 3.5: toasts dismiss themselves after 6 s; an error stays until the
// reader dismisses it, and so does a toast that carries an action.
const TOAST_MS = 6000;

// providerLabel names a provider id in the sign-in button's label. The
// action carries only the id, so the label capitalizes it.
function providerLabel(provider: string): string {
  return provider.charAt(0).toUpperCase() + provider.slice(1);
}

export function Toasts() {
  const toasts = state.value.toasts;
  useEffect(() => {
    const timers = toasts
      .filter((t) => t.tone !== 'error' && t.action === undefined)
      .map((t) => window.setTimeout(() => actions.dismissToast(t.id), TOAST_MS));
    return () => timers.forEach((id) => window.clearTimeout(id));
  }, [toasts]);
  return (
    <ol class="toasts" aria-live="polite">
      {toasts.map((t) => {
        const action = t.action;
        return (
          <li key={t.id} class="toast holo flicker-once" data-tone={t.tone}>
            <span class="toast__glyph" aria-hidden="true">{t.tone === 'error' ? '✕' : t.tone === 'warn' ? '!' : '◈'}</span>
            <span>{t.text}</span>
            {action === 'restore-effects' && (
              <button
                type="button"
                class="btn btn--ghost"
                onClick={() => {
                  restoreEffects();
                  actions.dismissToast(t.id);
                }}
              >Restore effects</button>
            )}
            {action !== undefined && action !== 'restore-effects' && (
              <button
                type="button"
                class="btn btn--ghost"
                aria-label={`Sign in to ${providerLabel(action.provider)}`}
                onClick={() => {
                  openSignIn(action.provider, action.model);
                  actions.dismissToast(t.id);
                }}
              >Sign in</button>
            )}
            <button type="button" class="toast__close" aria-label="Dismiss notification" onClick={() => actions.dismissToast(t.id)}>×</button>
          </li>
        );
      })}
    </ol>
  );
}

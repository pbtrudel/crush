import { useEffect } from 'preact/hooks';
import { actions, state } from '../state/store';

export function Toasts() {
  const toasts = state.value.toasts;
  useEffect(() => {
    const timers = toasts.map((t) => window.setTimeout(() => actions.dismissToast(t.id), t.tone === 'error' ? 8000 : 4000));
    return () => timers.forEach((id) => window.clearTimeout(id));
  }, [toasts]);
  return (
    <ol class="toasts" aria-live="polite">
      {toasts.map((t) => (
        <li key={t.id} class="toast" data-tone={t.tone}>
          <span class="toast__glyph" aria-hidden="true">{t.tone === 'error' ? '✕' : t.tone === 'warn' ? '!' : '◈'}</span>
          <span>{t.text}</span>
          <button type="button" class="toast__close" aria-label="Dismiss notification" onClick={() => actions.dismissToast(t.id)}>×</button>
        </li>
      ))}
    </ol>
  );
}

import { state } from '../state/store';

// The HUD link is the one polite live region for the connection; this banner
// repeats it for sighted users, so it stays out of the accessibility tree.
export function ConnectionBanner() {
  const c = state.value.conn;
  if (c === 'reconnecting') {
    return <div class="banner banner--warn" aria-hidden="true">Signal lost. Reconnecting to crush.</div>;
  }
  if (c === 'ended') {
    return (
      <div class="banner banner--ended" aria-hidden="true">
        Session ended. The log stays readable. Start crush with <code>--web</code> or run <code>/web</code> to link a new tab.
      </div>
    );
  }
  return null;
}

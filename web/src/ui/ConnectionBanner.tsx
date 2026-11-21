import { state } from '../state/store';

export function ConnectionBanner() {
  const c = state.value.conn;
  if (c === 'reconnecting') {
    return <div class="banner banner--warn" role="status">Signal lost. Reconnecting to crush.</div>;
  }
  if (c === 'ended') {
    return (
      <div class="banner banner--ended" role="status">
        Session ended. The log stays readable. Start crush with <code>--web</code> or run <code>/web</code> to link a new tab.
      </div>
    );
  }
  return null;
}

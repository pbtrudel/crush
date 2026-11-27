import { openSessions } from '../../state/palette';
import { actions, state } from '../../state/store';

// PinToggle unpins the tab to browse another session, and says which one
// it reads until the tab returns. With one root session and nothing
// browsed there is nothing to choose, so the HUD keeps the toggle out.
//
// It sits in the servers column's empty band under the instruments, so the
// model line and the instruments keep their width and the strip keeps its
// height at every layout. The button stands at least 24px tall for the
// pointer. Its visible word is short enough not to widen the servers column,
// and its accessible name keeps the full state.
export function PinToggle() {
  const s = state.value;
  if (s.rootSessions <= 1 && s.browsing === null) return null;
  if (s.browsing !== null) {
    const browsed = `Browsing ${s.session?.title || 'another session'}`;
    return (
      <span class="hud__pin-slot" role="group" aria-label="Session pin">
        <button
          type="button"
          class="hud__pin-btn"
          aria-label={browsed}
          title={browsed}
          aria-pressed="false"
          onClick={() => openSessions()}
        >Browsing</button>
        <button
          type="button"
          class="hud__pin-btn hud__pin-return"
          aria-label="Return to the terminal's session"
          title="Return to the terminal's session"
          onClick={() => void actions.repin()}
        >Return</button>
      </span>
    );
  }
  return (
    <button
      type="button"
      class="hud__pin-slot hud__pin-btn"
      aria-label="Following the terminal"
      title="Following the terminal"
      aria-pressed="true"
      onClick={() => openSessions()}
    >Following</button>
  );
}

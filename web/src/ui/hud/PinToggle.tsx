import { openSessions } from '../../state/palette';
import { actions, state } from '../../state/store';

// PinToggle unpins the tab to browse another session, and says which one
// it reads until the tab returns. With one root session and nothing
// browsed there is nothing to choose, so the HUD keeps the toggle out.
//
// It takes the strip's second row rather than a column, so the model line
// and the instruments keep their width at every layout. The visible text is
// the accessible name; the button stands at least 24px tall for the pointer.
export function PinToggle() {
  const s = state.value;
  if (s.rootSessions <= 1 && s.browsing === null) return null;
  if (s.browsing !== null) {
    return (
      <span class="hud__pin-slot" role="group" aria-label="Session pin">
        <button
          type="button"
          class="hud__pin-btn"
          aria-pressed="false"
          onClick={() => openSessions()}
        >Browsing {s.session?.title || 'another session'}</button>
        <button
          type="button"
          class="hud__pin-btn hud__pin-return"
          onClick={() => void actions.repin()}
        >Return to the terminal's session</button>
      </span>
    );
  }
  return (
    <button
      type="button"
      class="hud__pin-slot hud__pin-btn"
      aria-pressed="true"
      onClick={() => openSessions()}
    >Following the terminal</button>
  );
}

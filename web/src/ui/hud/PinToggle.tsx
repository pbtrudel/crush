import { openSessions } from '../../state/palette';
import { actions, state } from '../../state/store';

// PinToggle unpins the tab to browse another session, and says which one
// it reads until the tab returns. With one root session and nothing
// browsed there is nothing to choose, so the HUD keeps the toggle out.
//
// Pinned, it is a compact glyph: the HUD strip has no room for a second
// full label beside the session title, and the tile keeps the words. While
// browsing it takes its own line under the model line, which is only
// drawn when the tab is off the terminal's session.
export function PinToggle() {
  const s = state.value;
  if (s.rootSessions <= 1 && s.browsing === null) return null;
  if (s.browsing !== null) {
    return (
      <span class="hud__pin" role="group" aria-label="Session pin">
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
      class="hud__pin-toggle"
      aria-pressed="true"
      aria-label="Following the terminal"
      title="Following the terminal. Open the session switcher to browse another session."
      onClick={() => openSessions()}
    >
      <svg aria-hidden="true" viewBox="0 0 16 16" width="12" height="12">
        <path d="M8 1a4.5 4.5 0 0 0-4.5 4.5C3.5 9 8 15 8 15s4.5-6 4.5-9.5A4.5 4.5 0 0 0 8 1zm0 6a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z" />
      </svg>
    </button>
  );
}

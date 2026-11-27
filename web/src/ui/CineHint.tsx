import { cinematic } from '../state/cinematic';
import { state } from '../state/store';

// aria-hidden: the focused canvas already carries the same words in its
// label, so this is for sighted keyboard users only.
export function CineHint() {
  if (!cinematic.value || !state.value.display.cinematic_hint) return null;
  return <p class="cine-hint holo hud-label" aria-hidden="true">Drag or use Shift + arrow keys to orbit. Esc leaves.</p>;
}

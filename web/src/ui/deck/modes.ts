import { deckMode } from '../../state/palette';

export type DeckMode = 'prompt' | 'shell' | 'note';

// modeFor names the mode the deck submits in. Note mode is sticky until it
// is left, while shell mode is derived from the text: a leading bang with no
// trim, the terminal's rule.
export function modeFor(text: string, sticky: 'note' | null): DeckMode {
  if (sticky === 'note') return 'note';
  return text.startsWith('!') ? 'shell' : 'prompt';
}

// payloadFor turns the deck text into what the route receives: a shell
// command loses its bang, everything else travels as typed.
export function payloadFor(mode: DeckMode, text: string): string {
  return mode === 'shell' && text.startsWith('!') ? text.slice(1) : text;
}

// leaveNoteMode clears the sticky mode, which Escape and the palette's
// shell action both do.
export function leaveNoteMode(): void {
  if (deckMode.value !== null) deckMode.value = null;
}

// focusDeck hands the keyboard back to the prompt, the one target every
// surface returns to.
export function focusDeck(): void {
  const el = document.getElementById('deck-input');
  if (el instanceof HTMLTextAreaElement) el.focus();
}

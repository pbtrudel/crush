import { signal } from '@preact/signals';

export type PaletteSection = 'all' | 'sessions' | 'models' | 'commands' | 'skills';

// paletteOpen holds the section the palette shows; null means it is closed.
export const paletteOpen = signal<PaletteSection | null>(null);
export const paletteQuery = signal('');
// argumentItem is the id of the item whose argument form is open, so the
// App's Escape handler can hand the list back before closing the dialog.
export const argumentItem = signal<string | null>(null);

export function openPalette(section: PaletteSection = 'all', query = ''): void {
  paletteQuery.value = query;
  argumentItem.value = null;
  paletteOpen.value = section;
}

export function closePalette(): void {
  paletteOpen.value = null;
  paletteQuery.value = '';
  argumentItem.value = null;
}

export function togglePalette(section: PaletteSection = 'all'): void {
  if (paletteOpen.value === null) openPalette(section);
  else closePalette();
}

export function setPaletteQuery(query: string): void {
  paletteQuery.value = query;
}

// escapePalette closes the innermost palette layer: an open argument form
// hands the list back, otherwise the dialog closes and its focus trap
// restores the opener. It reports whether it consumed the press.
export function escapePalette(): boolean {
  if (paletteOpen.value === null) return false;
  if (argumentItem.value !== null) {
    argumentItem.value = null;
    return true;
  }
  closePalette();
  return true;
}

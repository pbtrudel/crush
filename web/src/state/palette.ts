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

// sessionsOpen holds the HUD's session switcher. Its inline rename and delete
// confirm live here too, so the App's Escape handler can cancel those before
// it closes the dialog, as the palette's argument form does.
export const sessionsOpen = signal(false);
export const sessionRenameId = signal<string | null>(null);
export const sessionDeleteId = signal<string | null>(null);

export function openSessions(): void {
  sessionRenameId.value = null;
  sessionDeleteId.value = null;
  sessionsOpen.value = true;
}

export function closeSessions(): void {
  sessionsOpen.value = false;
  sessionRenameId.value = null;
  sessionDeleteId.value = null;
}

// escapeSessions closes the innermost switcher layer: an open rename or
// delete confirm is cancelled first, otherwise the dialog closes and its
// focus trap restores the opener. It reports whether it consumed the press.
export function escapeSessions(): boolean {
  if (!sessionsOpen.value) return false;
  if (sessionRenameId.value !== null) {
    sessionRenameId.value = null;
    return true;
  }
  if (sessionDeleteId.value !== null) {
    sessionDeleteId.value = null;
    return true;
  }
  closeSessions();
  return true;
}

// modelsOpen holds the HUD's model selector. It opens from the HUD model
// name and from the palette, and it has no inner editor, so Escape closes
// it whole.
export const modelsOpen = signal(false);
// modelsFocus is where the selector puts the focus when it opens: the
// reasoning control the palette asks for, or the filter.
export const modelsFocus = signal<'reasoning' | null>(null);

export function openModels(focus?: 'reasoning'): void {
  modelsFocus.value = focus ?? null;
  modelsOpen.value = true;
}

export function closeModels(): void {
  modelsOpen.value = false;
  modelsFocus.value = null;
}

// escapeModels reports whether it consumed the press.
export function escapeModels(): boolean {
  if (!modelsOpen.value) return false;
  closeModels();
  return true;
}

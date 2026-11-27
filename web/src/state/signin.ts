import { signal } from '@preact/signals';
import type { ModelTarget } from '../api/types';
import { closePanels } from './palette';

// signInOpen is the sign-in dialog's open state, or null when it is closed.
// A provider preselects the row; a model asks the provider sign-in to apply
// that model once the credential is saved.
export const signInOpen = signal<{ provider?: string; model?: ModelTarget } | null>(null);

// openSignIn closes the palette and the HUD panels first: a toast above them
// can open this dialog, and two layers with two backdrops must never stack.
export function openSignIn(provider?: string, model?: ModelTarget): void {
  closePanels();
  signInOpen.value = model ? { provider, model } : { provider };
}

export function closeSignIn(): void {
  signInOpen.value = null;
}

// signInModel is the model the dialog applies once the credential is
// saved, when the re-auth toast named one.
export function signInModel(): ModelTarget | undefined {
  return signInOpen.value?.model;
}

// escapeSignIn handles Escape while the dialog is open and reports whether
// it consumed the key, so the layer below never sees it.
export function escapeSignIn(): boolean {
  if (!signInOpen.value) return false;
  closeSignIn();
  return true;
}

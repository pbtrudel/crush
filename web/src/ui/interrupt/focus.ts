import { useEffect } from 'preact/hooks';
import { paletteReturnFocus } from '../../state/palette';
import { focusDeck } from '../deck/modes';

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'textarea:not([disabled])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ');

function controls(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE));
}

// useDialogFocus keeps Tab inside a dialog, focuses its first control
// whenever focusKey changes, and hands focus back to the element that had it
// when the dialog opened. The dialog root needs tabIndex={-1} so it can hold
// focus when every control is disabled.
export function useDialogFocus(root: { current: HTMLElement | null }, focusKey: string) {
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab') return;
      const items = controls(el);
      if (items.length === 0) {
        e.preventDefault();
        el.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (active === el || !el.contains(active)) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
        return;
      }
      if (e.shiftKey && active === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    };
    el.addEventListener('keydown', onKey);
    return () => {
      el.removeEventListener('keydown', onKey);
      // The opener may be gone with the session; only restore a live element.
      if (previous?.isConnected) previous.focus();
    };
  }, [root]);

  useEffect(() => {
    const el = root.current;
    if (!el) return;
    (controls(el)[0] ?? el).focus();
  }, [root, focusKey]);
}

// usePaletteDeckFocus hands the focus to the deck when a dialog the palette
// opened closes. The dialog's own trap restores the body, which was all the
// palette left behind as its opener.
export function usePaletteDeckFocus() {
  const fromPalette = paletteReturnFocus.value;
  useEffect(() => () => {
    if (!fromPalette) return;
    paletteReturnFocus.value = false;
    // After the trap's own restore, which runs as this commit unmounts.
    queueMicrotask(focusDeck);
  }, []);
}

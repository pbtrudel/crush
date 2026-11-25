import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { paletteReturnFocus } from '../../state/palette';
import { needsAttention } from '../../state/reducer';
import { state } from '../../state/store';
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
// when the dialog opened. A control that leaves the tree while it holds the
// focus passes it to the control the dialog remembers, and a dialog under an
// interrupt that needs the user takes the focus back when it closes, on the
// control that had it. inert says an interrupt above the page covers the
// dialog and owns the focus. The dialog root needs tabIndex={-1} so it can
// hold focus when every control is disabled.
export function useDialogFocus(root: { current: HTMLElement | null }, focusKey: string, inert = false) {
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

  // lastInside names the control inside the dialog that last held the focus.
  // It is recorded from focusin, in a layout effect so the listener is there
  // before the second effect takes the first focus: tabbing into a field
  // renders nothing either effect could read.
  const lastInside = useRef<HTMLElement | null>(null);
  const hadFocus = useRef(false);
  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    const onFocusIn = (e: FocusEvent) => {
      if (!(e.target instanceof HTMLElement)) return;
      lastInside.current = e.target;
      hadFocus.current = true;
    };
    el.addEventListener('focusin', onFocusIn);
    return () => el.removeEventListener('focusin', onFocusIn);
  }, [root]);

  // A button that unmounts while it holds the focus (a Retry the answer
  // replaces) hands it to the remembered control. An interrupt above the
  // page is inert, and the browser drops the dialog's focus to the body: the
  // dialog is the layer that stays open, so it takes the focus back when the
  // interrupt closes, on the control that had it. The effect takes no
  // dependency array because a focused button that unmounts changes no
  // prop, so it has to run after every render.
  const wasInert = useRef(inert);
  useEffect(() => {
    const el = root.current;
    const recovered = wasInert.current;
    wasInert.current = inert;
    if (el === null) return;
    if (!hadFocus.current && !recovered) return;
    // A prompt that still needs the user owns the focus, even when a dropped
    // link has already made the page interactive again.
    if (inert || needsAttention(state.value)) return;
    // The dialog's own effects run after this hook and know the control they
    // want (a filter, a section heading), so they get the first word: a
    // microtask takes a focus that is still lost once they have run.
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled || el.contains(document.activeElement)) return;
      hadFocus.current = false;
      const remembered = lastInside.current;
      if (remembered !== null && remembered.isConnected && el.contains(remembered)) {
        remembered.focus();
        return;
      }
      (controls(el)[0] ?? el).focus();
    });
    return () => { cancelled = true; };
  });
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

import { computed, signal } from '@preact/signals';
import { drawerOpen } from './drawer';

// shell.css lays the open drawer over the log column between these widths, so
// the log underneath leaves the tab order until the drawer closes. Wider
// layouts give the drawer its own column and narrower ones a tab, and both
// keep the log reachable.
export const OVERLAY_QUERY = '(min-width: 1024px) and (max-width: 1279px)';

export const overlayWidth = signal(false);
export const logInert = computed(() => drawerOpen.value && overlayWidth.value);

// watchOverlayWidth publishes the media query and returns its teardown.
export function watchOverlayWidth(): () => void {
  if (typeof window.matchMedia !== 'function') return () => {};
  const query = window.matchMedia(OVERLAY_QUERY);
  overlayWidth.value = query.matches;
  const onChange = (e: MediaQueryListEvent) => { overlayWidth.value = e.matches; };
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

// focusDrawer puts the focus on the drawer's close button, or on its handle
// when the drawer has none.
function focusDrawer(): void {
  const close = document.querySelector<HTMLElement>('#file-drawer .drawer__head button');
  (close ?? document.querySelector<HTMLElement>('.drawer-handle'))?.focus();
}

// handOffLogFocus moves the focus the covered log would otherwise keep on an
// inert control: the drawer takes it when it opens over the log, and gives it
// back to the handle when it closes over it. `wasCovered` is the state the
// effect saw before, so neither layout switch fires on its own.
export function handOffLogFocus(covered: boolean, wasCovered: boolean): void {
  const active = document.activeElement;
  if (active === null || active === document.body) return;
  const drawer = document.querySelector('#file-drawer');
  const handle = document.querySelector<HTMLElement>('.drawer-handle');
  if (covered) {
    const log = document.querySelector('.panel--log');
    // The handle is the drawer's own keyboard route, so it hands over too.
    if (!(log?.contains(active) ?? false) && active !== handle) return;
    focusDrawer();
    return;
  }
  if (wasCovered && drawer !== null && drawer.contains(active)) handle?.focus();
}

// watchFocusHandoff catches the focus the browser drops when a panel leaves
// the tab order under it: the blur to the body happens as inert lands, before
// the effect above can read activeElement, but the focusout still carries the
// node it left. The effect covers engines that leave the focus in place.
export function watchFocusHandoff(): () => void {
  const onFocusOut = (e: FocusEvent) => {
    const target = e.target;
    // Focus that moved to a real element is the user's, not a blur.
    if (!(target instanceof Element) || e.relatedTarget !== null) return;
    if (target.closest('.panel--log') !== null) {
      if (logInert.value) focusDrawer();
      return;
    }
    // Closing the drawer under its own controls gives the handle the focus
    // back instead of the body.
    if (target.closest('#file-drawer') !== null) document.querySelector<HTMLElement>('.drawer-handle')?.focus();
  };
  document.addEventListener('focusout', onFocusOut, true);
  return () => document.removeEventListener('focusout', onFocusOut, true);
}

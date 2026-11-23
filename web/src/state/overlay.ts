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

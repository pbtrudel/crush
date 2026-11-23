import { afterEach, describe, expect, it, vi } from 'vitest';
import { drawerOpen } from './drawer';
import { logInert, overlayWidth, watchOverlayWidth } from './overlay';

type Listener = (e: MediaQueryListEvent) => void;

function stubMatchMedia(matches: boolean) {
  const listeners = new Set<Listener>();
  const query = {
    matches,
    media: '',
    addEventListener: (_type: string, fn: Listener) => { listeners.add(fn); },
    removeEventListener: (_type: string, fn: Listener) => { listeners.delete(fn); },
  };
  window.matchMedia = vi.fn(() => query) as unknown as typeof window.matchMedia;
  return {
    fire: (next: boolean) => {
      query.matches = next;
      for (const fn of listeners) fn({ matches: next } as MediaQueryListEvent);
    },
    listeners,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  drawerOpen.value = false;
  overlayWidth.value = false;
});

describe('the covered log', () => {
  it('is inert only while the overlay drawer covers it', () => {
    drawerOpen.value = true;
    expect(logInert.value).toBe(false);
    overlayWidth.value = true;
    expect(logInert.value).toBe(true);
    drawerOpen.value = false;
    expect(logInert.value).toBe(false);
  });

  it('follows the overlay width from the media query and stops on cleanup', () => {
    const media = stubMatchMedia(true);
    const stop = watchOverlayWidth();
    expect(overlayWidth.value).toBe(true);
    expect(window.matchMedia).toHaveBeenCalledWith('(min-width: 1024px) and (max-width: 1279px)');

    media.fire(false);
    expect(overlayWidth.value).toBe(false);

    stop();
    expect(media.listeners.size).toBe(0);
    media.fire(true);
    expect(overlayWidth.value).toBe(false);
  });
});

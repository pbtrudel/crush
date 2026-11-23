import { afterEach, describe, expect, it, vi } from 'vitest';
import { drawerOpen } from './drawer';
import { handOffLogFocus, logInert, overlayWidth, watchFocusHandoff, watchOverlayWidth } from './overlay';

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

function buildDrawerDom(withClose = true) {
  document.body.innerHTML = [
    '<section class="panel--log"><button id="log-control">log</button></section>',
    '<button class="drawer-handle" id="handle">files</button>',
    '<aside id="file-drawer"><header class="drawer__head">',
    withClose ? '<button id="drawer-close">x</button>' : '<span>Edited files</span>',
    '</header></aside>',
    '<button id="outside">elsewhere</button>',
  ].join('');
  const one = <T extends HTMLElement>(selector: string) => document.querySelector<T>(selector);
  return {
    control: one<HTMLButtonElement>('#log-control')!,
    handle: one<HTMLButtonElement>('#handle')!,
    close: one<HTMLButtonElement>('#drawer-close'),
    outside: one<HTMLButtonElement>('#outside')!,
  };
}

describe('the focus a covered log would lose', () => {
  afterEach(() => { document.body.innerHTML = ''; });

  it('moves from the log to the drawer close button', () => {
    const dom = buildDrawerDom();
    dom.control.focus();
    handOffLogFocus(true, false);
    expect(document.activeElement).toBe(dom.close);
  });

  it('falls back to the handle when the drawer has no close button', () => {
    const dom = buildDrawerDom(false);
    dom.control.focus();
    handOffLogFocus(true, false);
    expect(document.activeElement).toBe(dom.handle);
  });

  it('takes over from the handle that opened the drawer', () => {
    const dom = buildDrawerDom();
    dom.handle.focus();
    handOffLogFocus(true, false);
    expect(document.activeElement).toBe(dom.close);
  });

  it('gives the handle the focus back when the drawer closes over it', () => {
    const dom = buildDrawerDom();
    dom.close!.focus();
    handOffLogFocus(false, true);
    expect(document.activeElement).toBe(dom.handle);
  });

  it('leaves the focus alone when it is somewhere else', () => {
    const dom = buildDrawerDom();
    dom.outside.focus();
    handOffLogFocus(true, false);
    expect(document.activeElement).toBe(dom.outside);
    handOffLogFocus(false, true);
    expect(document.activeElement).toBe(dom.outside);
  });

  it('does not move the focus without a transition', () => {
    const dom = buildDrawerDom();
    dom.close!.focus();
    handOffLogFocus(false, false);
    expect(document.activeElement).toBe(dom.close);
  });
});

describe('the focus the browser drops with the panel', () => {
  afterEach(() => { document.body.innerHTML = ''; });

  it('takes the focus the log dropped on the body', () => {
    const dom = buildDrawerDom();
    const stop = watchFocusHandoff();
    try {
      drawerOpen.value = true;
      overlayWidth.value = true;
      dom.control.focus();
      dom.control.blur();
      expect(document.activeElement).toBe(dom.close);
    } finally {
      stop();
    }
  });

  it('gives the handle the focus the drawer dropped on the body', () => {
    const dom = buildDrawerDom();
    const stop = watchFocusHandoff();
    try {
      dom.close!.focus();
      dom.close!.blur();
      expect(document.activeElement).toBe(dom.handle);
    } finally {
      stop();
    }
  });

  it('leaves a focus that moved somewhere real alone', () => {
    const dom = buildDrawerDom();
    const stop = watchFocusHandoff();
    try {
      drawerOpen.value = true;
      overlayWidth.value = true;
      dom.control.focus();
      dom.outside.focus();
      expect(document.activeElement).toBe(dom.outside);
    } finally {
      stop();
    }
  });
});

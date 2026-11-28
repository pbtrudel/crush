import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, beforeEach } from 'vitest'

import {
  listenQuery,
  readMatches,
  resetMediaQueries,
  unlistenQuery,
  type MediaQueryListener,
} from './mediaQueries'

// jsdom has no matchMedia, and the frame asks it for the drawer breakpoint
// and the system theme. The records live in mediaQueries.ts, so a test can
// drive a change event with setMediaQueryMatches.
if (typeof window.matchMedia !== 'function') {
  window.matchMedia = (query: string) =>
    ({
      get matches() {
        return readMatches(query)
      },
      media: query,
      onchange: null,
      addEventListener: (_type: string, listener: MediaQueryListener) =>
        listenQuery(query, listener),
      removeEventListener: (_type: string, listener: MediaQueryListener) =>
        unlistenQuery(query, listener),
      addListener: (listener: MediaQueryListener) => listenQuery(query, listener),
      removeListener: (listener: MediaQueryListener) => unlistenQuery(query, listener),
      dispatchEvent: () => false,
    }) as MediaQueryList
}

// jsdom has no pointer capture, and the sidebar resizer captures the pointer
// for the length of a drag.
if (typeof Element.prototype.setPointerCapture !== 'function') {
  Element.prototype.setPointerCapture = () => undefined
  Element.prototype.releasePointerCapture = () => undefined
  Element.prototype.hasPointerCapture = () => false
}

// jsdom has no Element.scrollTo, and the virtualizer scrolls the log with
// it. The stub writes scrollTop, which a test's scrollState records.
if (typeof Element.prototype.scrollTo !== 'function') {
  Element.prototype.scrollTo = function scrollTo(
    this: Element,
    options?: ScrollToOptions | number,
    y?: number,
  ) {
    const top = typeof options === 'number' ? y : options?.top
    if (typeof top === 'number') {
      this.scrollTop = top
    }
  } as Element['scrollTo']
}

beforeEach(() => {
  resetMediaQueries()
})

// Vitest runs without globals, so testing-library cannot register its own
// cleanup. The theme the frame applied to <html> must not leak between
// tests either.
afterEach(() => {
  cleanup()
  delete document.documentElement.dataset.dsTheme
})

// The jsdom environment has no matchMedia, so setup.ts installs the stub
// below. Each query keeps one shared record here, so a test can drive the
// change event with setMediaQueryMatches and every listener the components
// registered fires, and resetMediaQueries drops the records between tests.

export type MediaQueryListener = (event: MediaQueryListEvent) => void

interface QueryState {
  matches: boolean
  listeners: Set<MediaQueryListener>
}

const queries = new Map<string, QueryState>()

function stateFor(query: string): QueryState {
  const existing = queries.get(query)
  if (existing) {
    return existing
  }
  const created: QueryState = { matches: false, listeners: new Set() }
  queries.set(query, created)
  return created
}

export function readMatches(query: string): boolean {
  return stateFor(query).matches
}

export function listenQuery(query: string, listener: MediaQueryListener): void {
  stateFor(query).listeners.add(listener)
}

export function unlistenQuery(query: string, listener: MediaQueryListener): void {
  stateFor(query).listeners.delete(listener)
}

// setMediaQueryMatches flips a query and fires its change listeners, the way
// a window crossing a breakpoint or a system theme change does.
export function setMediaQueryMatches(query: string, matches: boolean): void {
  const state = stateFor(query)
  state.matches = matches
  const event = { matches, media: query } as unknown as MediaQueryListEvent
  for (const listener of [...state.listeners]) {
    listener(event)
  }
}

export function resetMediaQueries(): void {
  queries.clear()
}

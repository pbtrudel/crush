import { createContext, useMemo, useState, type ReactNode } from 'react'

import type { AppState, AppStore, WireMessage } from '../store/app'
import type { Store } from '../store/store'
import { createDiffCache, type DiffCache } from './diffCache'

export type ChatStore = Store<AppState> & AppStore

// RenderTranscript draws a subagent's messages with the chat's own message
// view. It is handed down instead of imported, so the tool views never
// import the message view that imports them.
export type RenderTranscript = (messages: WireMessage[], depth: number, turnActive: boolean) => ReactNode

// IdSet is a small store over a set of ids. A row subscribes and reads its
// own id, so a toggle wakes only the components whose answer changed: the
// context that carries it never changes, and no sibling re-renders.
export interface IdSet {
  has(id: string): boolean
  subscribe(listener: () => void): () => void
  toggle(id: string): void
}

export interface ToolEnvironment {
  mcpServers: readonly string[]
  store: ChatStore | null
  renderTranscript: RenderTranscript | null
  // expanded holds the open rows by call id. It lives above the rows, so a
  // row the virtualized log unmounts and mounts again keeps its state.
  expanded: IdSet
  // revealed holds the output blocks the person asked to see whole, by
  // block key, so a revealed result survives a remount too.
  revealed: IdSet
  // diffs keeps each edit's diff by call id for the session on screen, so a
  // row the log unmounts and mounts again does not run jsdiff a second time.
  diffs: DiffCache
  toggle(callId: string): void
  reveal(key: string): void
}

const noServers: readonly string[] = []
const nothing: ReadonlySet<string> = new Set()

// createIdSet builds one mutable id set and its subscription. The set is
// replaced on every change, so a listener that reads it never sees a value
// it has already handled.
function createIdSet(): IdSet {
  let ids: ReadonlySet<string> = nothing
  const listeners = new Set<() => void>()
  return {
    has: (id) => ids.has(id),
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    toggle: (id) => {
      const next = new Set(ids)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      ids = next
      for (const listener of listeners) {
        listener()
      }
    },
  }
}

// inertIds is the default context's set: a row rendered without a provider
// reads nothing and toggles nothing.
const inertIds: IdSet = {
  has: () => false,
  subscribe: () => () => undefined,
  toggle: () => undefined,
}

// noDiffCache is the default context's cache: a row rendered without a
// provider computes its diff and keeps nothing.
const noDiffCache: DiffCache = {
  editDiff: (_callId, _filePair, _callPairs, compute) => compute(),
}

export const ToolEnvironmentContext = createContext<ToolEnvironment>({
  mcpServers: noServers,
  store: null,
  renderTranscript: null,
  expanded: inertIds,
  revealed: inertIds,
  diffs: noDiffCache,
  toggle: () => undefined,
  reveal: () => undefined,
})

export interface ToolEnvironmentProviderProps {
  mcpServers?: readonly string[]
  store?: ChatStore | null
  renderTranscript?: RenderTranscript | null
  children: ReactNode
}

// ToolEnvironmentProvider carries what the tool views share. Its value is
// memoised and its state is created once, so opening a row or revealing an
// output never rebuilds the context: the rows subscribe to the set they care
// about instead, and one toggle re-renders one row.
//
// One provider is one session. The conversation renders it under a key of the
// selected session id, so switching sessions remounts it and its diff cache
// with it: no answer outlives the session it was computed for. A caller must
// pass stable values: the same mcpServers array and the same renderTranscript
// function on every render (a module constant, useMemo or useCallback). A
// fresh array or function would rebuild the context value and re-render every
// row in the log.
export function ToolEnvironmentProvider({
  mcpServers = noServers,
  store = null,
  renderTranscript = null,
  children,
}: ToolEnvironmentProviderProps) {
  const [scoped] = useState(() => ({
    expanded: createIdSet(),
    revealed: createIdSet(),
    diffs: createDiffCache(),
  }))
  const value = useMemo(
    () => ({
      mcpServers,
      store,
      renderTranscript,
      expanded: scoped.expanded,
      revealed: scoped.revealed,
      diffs: scoped.diffs,
      toggle: scoped.expanded.toggle,
      reveal: scoped.revealed.toggle,
    }),
    [mcpServers, store, renderTranscript, scoped],
  )
  return <ToolEnvironmentContext.Provider value={value}>{children}</ToolEnvironmentContext.Provider>
}

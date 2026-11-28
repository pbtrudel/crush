import { createContext, useCallback, useMemo, useState, type ReactNode } from 'react'

import type { AppState, AppStore, WireMessage } from '../store/app'
import type { Store } from '../store/store'

export type ChatStore = Store<AppState> & AppStore

// RenderTranscript draws a subagent's messages with the chat's own message
// view. It is handed down instead of imported, so the tool views never
// import the message view that imports them.
export type RenderTranscript = (messages: WireMessage[], depth: number, turnActive: boolean) => ReactNode

export interface ToolEnvironment {
  mcpServers: readonly string[]
  store: ChatStore | null
  renderTranscript: RenderTranscript | null
  // expanded holds the open rows by call id. It lives above the rows, so a
  // row the virtualized log unmounts and mounts again keeps its state.
  expanded: ReadonlySet<string>
  toggle(callId: string): void
}

const nothingOpen: ReadonlySet<string> = new Set()
const noServers: readonly string[] = []

export const ToolEnvironmentContext = createContext<ToolEnvironment>({
  mcpServers: noServers,
  store: null,
  renderTranscript: null,
  expanded: nothingOpen,
  toggle: () => undefined,
})

export interface ToolEnvironmentProviderProps {
  mcpServers?: readonly string[]
  store?: ChatStore | null
  renderTranscript?: RenderTranscript | null
  children: ReactNode
}

export function ToolEnvironmentProvider({
  mcpServers = noServers,
  store = null,
  renderTranscript = null,
  children,
}: ToolEnvironmentProviderProps) {
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(nothingOpen)
  const toggle = useCallback((callId: string) => {
    setExpanded((previous) => {
      const next = new Set(previous)
      if (next.has(callId)) {
        next.delete(callId)
      } else {
        next.add(callId)
      }
      return next
    })
  }, [])
  const value = useMemo(
    () => ({ mcpServers, store, renderTranscript, expanded, toggle }),
    [mcpServers, store, renderTranscript, expanded, toggle],
  )
  return <ToolEnvironmentContext.Provider value={value}>{children}</ToolEnvironmentContext.Provider>
}

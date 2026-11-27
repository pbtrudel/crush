import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ApiClient } from '../api/http'
import { emptySessionData, type AppState, type AppStore, type WireMessage } from '../store/app'
import { useStore, type Store } from '../store/store'
import Conversation from './Conversation'

export interface ConversationContainerProps {
  client: ApiClient
  store: Store<AppState> & AppStore
}

// MessagesPage is what GET /api/sessions/{sid}/messages answers: the page
// before the given message, oldest first, and whether more history exists.
interface MessagesPage {
  messages: WireMessage[]
  has_more: boolean
}

// messagePageLimit is the page size the tab asks for. It is explicit so the
// amount of history on screen does not depend on the gateway's default.
const messagePageLimit = 100

// ConversationContainer wires the presentational conversation to the store
// and the gateway. The snapshot's last page comes from the store; the pages
// before it are read on demand and kept here, because nothing else needs
// them.
export default function ConversationContainer({ client, store }: ConversationContainerProps) {
  const sessionId = useStore(store, (state) => state.selectedSessionId)
  const title = useStore(
    store,
    (state) => state.sessions.find((row) => row.id === state.selectedSessionId)?.title ?? '',
  )
  const data = useStore(store, (state) =>
    state.selectedSessionId ? state.sessionData[state.selectedSessionId] : undefined,
  )
  // A resync replaces the bootstrap, and the snapshots that follow it are new
  // pages of history: what was read under the old one cannot be reused.
  const bootstrap = useStore(store, (state) => state.bootstrap)
  const snapshotMessages = data?.messages
  const [older, setOlder] = useState<WireMessage[]>([])
  // olderHasMore is the answer the last page gave; null means no page has
  // been loaded yet, and the snapshot's own has_more decides.
  const [olderHasMore, setOlderHasMore] = useState<boolean | null>(null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // epoch names the snapshot the loaded pages belong to. A reset bumps it, so
  // a page that comes back after the snapshot it was read against was
  // replaced is dropped instead of mixed into the new one.
  const epoch = useRef(0)

  const dropPages = useCallback(() => {
    epoch.current += 1
    setOlder((pages) => (pages.length === 0 ? pages : []))
    setOlderHasMore(null)
    setLoadingOlder(false)
    setError((previous) => (previous === null ? previous : null))
  }, [])

  // A new selection starts from its snapshot, and a resync replaces it, so
  // the pages loaded for the old one are dropped.
  useEffect(() => {
    dropPages()
  }, [dropPages, sessionId, bootstrap])

  // A snapshot rebuilds every message from the wire, so not one object of the
  // previous list survives; a live frame keeps the messages it did not touch.
  // Seeing a rebuilt list means the pages on screen were read against a
  // snapshot that is gone, so they go with it.
  const previousSnapshot = useRef<WireMessage[] | null>(null)
  useEffect(() => {
    const previous = previousSnapshot.current
    previousSnapshot.current = snapshotMessages ?? null
    if (previous === null || snapshotMessages === undefined || previous === snapshotMessages) {
      return
    }
    if (snapshotMessages.length > 0 && snapshotMessages[0] === previous[0]) {
      return
    }
    dropPages()
  }, [dropPages, snapshotMessages])

  const loadOlder = useCallback(() => {
    if (!sessionId) {
      return
    }
    const current = store.get().sessionData[sessionId] ?? emptySessionData
    // The next page ends just before the oldest message already on screen,
    // whichever page it came from.
    const before = (older[0] ?? current.messages[0])?.id
    const query = before
      ? `?before=${encodeURIComponent(before)}&limit=${messagePageLimit}`
      : `?limit=${messagePageLimit}`
    const at = epoch.current
    setLoadingOlder(true)
    setError(null)
    void client
      .get<MessagesPage>(`/api/sessions/${encodeURIComponent(sessionId)}/messages${query}`)
      .then((page) => {
        if (epoch.current !== at) {
          return
        }
        // A page is older than the ones on screen, so it goes in front of
        // them; an overlapping message is already there and stays once.
        setOlder((previous) => prependPage(previous, page.messages))
        setOlderHasMore(page.has_more)
      })
      .catch((cause: unknown) => {
        if (epoch.current !== at) {
          return
        }
        setError(message(cause))
      })
      .finally(() => {
        if (epoch.current === at) {
          setLoadingOlder(false)
        }
      })
  }, [client, older, sessionId, store])

  // A resync can echo a message the tab already paged in, so the first copy
  // wins and the order stays oldest first.
  const messages = useMemo(() => {
    const seen = new Set<string>()
    const out: WireMessage[] = []
    for (const entry of [...older, ...(snapshotMessages ?? [])]) {
      if (seen.has(entry.id)) {
        continue
      }
      seen.add(entry.id)
      out.push(entry)
    }
    return out
  }, [older, snapshotMessages])

  const hasMore = olderHasMore === null ? (data?.hasMore ?? false) : olderHasMore

  return (
    <Conversation
      sessionId={sessionId}
      title={title}
      messages={messages}
      hasMore={hasMore}
      loadingOlder={loadingOlder}
      error={error}
      onLoadOlder={loadOlder}
    />
  )
}

// prependPage puts a page of older messages in front of the pages already
// loaded and drops the messages that are already there.
function prependPage(previous: WireMessage[], page: WireMessage[]): WireMessage[] {
  const seen = new Set(previous.map((entry) => entry.id))
  const fresh = page.filter((entry) => !seen.has(entry.id))
  if (fresh.length === 0) {
    return previous
  }
  return [...fresh, ...previous]
}

// message is the gateway's own sentence for a refused request. The token and
// the request never reach it.
function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

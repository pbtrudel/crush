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

// noPages is the empty list of a session with nothing paged in. It is one
// value, so an empty list keeps its identity between renders.
const noPages: WireMessage[] = []

// ConversationContainer hands the selected session to its own conversation.
// The paging state belongs to one session, so a new selection renders a new
// one rather than the old conversation with the new snapshot: that key is
// what keeps a page loaded for one session from appearing beside another,
// not even for a single render.
export default function ConversationContainer({ client, store }: ConversationContainerProps) {
  const sessionId = useStore(store, (state) => state.selectedSessionId)
  const title = useStore(
    store,
    (state) => state.sessions.find((row) => row.id === state.selectedSessionId)?.title ?? '',
  )
  return (
    <SessionConversation
      key={sessionId ?? ''}
      sessionId={sessionId}
      title={title}
      client={client}
      store={store}
    />
  )
}

interface SessionConversationProps extends ConversationContainerProps {
  sessionId: string | null
  title: string
}

// OlderPages is the history the tab read before the snapshot on screen.
interface OlderPages {
  // snapshotSeq is the snapshot these pages were read against. A page from
  // another snapshot is not shown at all, so a rebuilt list never mixes
  // with it, not even for the render before the effect clears it.
  snapshotSeq: number
  messages: WireMessage[]
  // hasMore is the answer the last page gave; null means none was read, and
  // the snapshot's own has_more decides.
  hasMore: boolean | null
  loading: boolean
  error: string | null
}

function emptyPages(snapshotSeq: number): OlderPages {
  return { snapshotSeq, messages: noPages, hasMore: null, loading: false, error: null }
}

function SessionConversation({ sessionId, title, client, store }: SessionConversationProps) {
  const data = useStore(store, (state) =>
    sessionId === null ? undefined : state.sessionData[sessionId],
  )
  // A resync replaces the bootstrap, and the snapshots that follow it are new
  // pages of history: what was read under the old one cannot be reused.
  const bootstrap = useStore(store, (state) => state.bootstrap)
  const snapshotSeq = data?.snapshotSeq ?? 0
  const snapshotMessages = data?.messages
  const [pages, setPages] = useState<OlderPages>(() => emptyPages(snapshotSeq))

  // Every snapshot and every resync replaces the history the loaded pages
  // were read against, so they go. The snapshot counter is what a rebuilt
  // list looks like from here: the message objects say nothing about it,
  // because a resubscribe can answer the very same page.
  const seen = useRef({ snapshotSeq, bootstrap })
  useEffect(() => {
    const previous = seen.current
    if (previous.snapshotSeq === snapshotSeq && previous.bootstrap === bootstrap) {
      return
    }
    seen.current = { snapshotSeq, bootstrap }
    setPages(emptyPages(snapshotSeq))
  }, [snapshotSeq, bootstrap])

  // Pages from another snapshot are not rendered, so the render before the
  // effect above runs still shows the snapshot alone.
  const current = pages.snapshotSeq === snapshotSeq
  const older = current ? pages.messages : noPages
  const hasMore = current && pages.hasMore !== null ? pages.hasMore : (data?.hasMore ?? false)
  const loadingOlder = current && pages.loading
  const error = current ? pages.error : null

  const loadOlder = useCallback(() => {
    if (sessionId === null) {
      return
    }
    const snapshot = store.get().sessionData[sessionId] ?? emptySessionData
    // The next page ends just before the oldest message already on screen,
    // whichever page it came from.
    const before = (older[0] ?? snapshot.messages[0])?.id
    const query = before
      ? `?before=${encodeURIComponent(before)}&limit=${messagePageLimit}`
      : `?limit=${messagePageLimit}`
    const at = snapshotSeq
    setPages((previous) =>
      previous.snapshotSeq === at ? { ...previous, loading: true, error: null } : previous,
    )
    void client
      .get<MessagesPage>(`/api/sessions/${encodeURIComponent(sessionId)}/messages${query}`)
      .then((page) => {
        setPages((previous) =>
          previous.snapshotSeq === at
            ? {
                ...previous,
                // A page is older than the ones on screen, so it goes in
                // front of them; an overlapping message stays there once.
                messages: prependPage(previous.messages, page.messages),
                hasMore: page.has_more,
                loading: false,
              }
            : previous,
        )
      })
      .catch((cause: unknown) => {
        setPages((previous) =>
          previous.snapshotSeq === at
            ? { ...previous, loading: false, error: message(cause) }
            : previous,
        )
      })
  }, [client, older, sessionId, snapshotSeq, store])

  // A resync can echo a message the tab already paged in, so the first copy
  // wins and the order stays oldest first.
  const messages = useMemo(() => {
    const seenIDs = new Set<string>()
    const out: WireMessage[] = []
    for (const entry of [...older, ...(snapshotMessages ?? [])]) {
      if (seenIDs.has(entry.id)) {
        continue
      }
      seenIDs.add(entry.id)
      out.push(entry)
    }
    return out
  }, [older, snapshotMessages])

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

import { useCallback, useMemo } from 'react'

import type { ApiClient } from '../api/http'
import { visibleMessages, type AppState, type AppStore, type MessagesPage, type WireMessage } from '../store/app'
import { useStore, type Store } from '../store/store'
import Conversation from './Conversation'

export interface ConversationContainerProps {
  client: ApiClient
  store: Store<AppState> & AppStore
}

// messagePageLimit is the page size the tab asks for. It is explicit so the
// amount of history on screen does not depend on the gateway's default.
const messagePageLimit = 100

// noPages is the empty list of a session with nothing paged in. It is one
// value, so an empty list keeps its identity between renders.
const noPages: WireMessage[] = []

// ConversationContainer hands the selected session to its own conversation.
// The log's scroll position belongs to one session, so a new selection
// renders a new one rather than the old conversation with the new snapshot:
// that key is what keeps one session's view from appearing beside another,
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

function SessionConversation({ sessionId, title, client, store }: SessionConversationProps) {
  const data = useStore(store, (state) => (sessionId === null ? undefined : state.sessionData[sessionId]))
  const older = data?.older ?? noPages
  const live = data?.messages ?? noPages
  const messages = useMemo(() => visibleMessages(older, live), [older, live])

  // The store owns the page and its generation: a page that answers after a
  // resync or a new snapshot is dropped there, so nothing here has to guess.
  const loadOlder = useCallback(() => {
    if (sessionId === null) {
      return
    }
    const ticket = store.beginOlderPage(sessionId)
    if (ticket === null) {
      return
    }
    const query = ticket.before
      ? `?before=${encodeURIComponent(ticket.before)}&limit=${messagePageLimit}`
      : `?limit=${messagePageLimit}`
    void client
      .get<MessagesPage>(`/api/sessions/${encodeURIComponent(sessionId)}/messages${query}`)
      .then((page) => store.applyOlderPage(ticket, page))
      .catch((cause: unknown) => store.failOlderPage(ticket, errorText(cause)))
  }, [client, sessionId, store])

  return (
    <Conversation
      sessionId={sessionId}
      title={title}
      messages={messages}
      hasMore={data?.hasMore ?? false}
      loadingOlder={data?.loadingOlder ?? false}
      error={data?.olderError ?? null}
      onLoadOlder={loadOlder}
    />
  )
}

// errorText is the gateway's own sentence for a refused request. The token
// and the request never reach it.
function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

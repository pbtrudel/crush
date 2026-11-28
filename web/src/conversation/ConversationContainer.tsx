import { useCallback, useMemo } from 'react'

import type { ApiClient } from '../api/http'
import { renderTranscript } from '../chat/Transcript'
import { visibleMessages, type AppState, type AppStore, type MessagesPage, type WireMessage } from '../store/app'
import { useStore, type Store } from '../store/store'
import { ToolEnvironmentProvider } from '../tools/environment'
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

  // The row keeps its identity until a frame changes it, so this selector
  // re-renders only on a real change.
  const row = useStore(store, (state) => state.sessions.find((entry) => entry.id === sessionId))
  const contextWindow = useStore(store, (state) => state.bootstrap?.model.context_window ?? 0)
  const usage = useMemo(
    () =>
      row === undefined
        ? null
        : {
            promptTokens: row.prompt_tokens ?? 0,
            completionTokens: row.completion_tokens ?? 0,
            cost: row.cost ?? 0,
            contextWindow,
          },
    [row, contextWindow],
  )
  // The bootstrap's MCP map keeps its identity until the next bootstrap,
  // so the server list is rebuilt only then: a fresh array on every render
  // would rebuild the tool environment and re-render every row in the log.
  const mcp = useStore(store, (state) => state.bootstrap?.mcp)
  const mcpServers = useMemo(() => Object.keys(mcp ?? {}), [mcp])

  // renderTranscript is the module's own function, so the environment's value
  // keeps its identity between renders and a streamed delta re-renders no
  // tool row but the one it changed.
  return (
    <ToolEnvironmentProvider
      sessionId={sessionId}
      mcpServers={mcpServers}
      store={store}
      renderTranscript={renderTranscript}
    >
      <Conversation
        sessionId={sessionId}
        title={title}
        messages={messages}
        hasMore={data?.hasMore ?? false}
        loadingOlder={data?.loadingOlder ?? false}
        error={data?.olderError ?? null}
        status={row?.status}
        usage={usage}
        onLoadOlder={loadOlder}
      />
    </ToolEnvironmentProvider>
  )
}

// errorText is the gateway's own sentence for a refused request. The token
// and the request never reach it.
function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

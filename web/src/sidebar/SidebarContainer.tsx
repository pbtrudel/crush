import { useCallback, useMemo, useState } from 'react'

import type { ApiClient } from '../api/http'
import { isUnread, type AppState, type AppStore } from '../store/app'
import { useStore, type Store } from '../store/store'
import Sidebar from './Sidebar'
import { sessionActions } from './actions'

// SidebarContainer wires the presentational sidebar to the store and the
// gateway. It deliberately holds no subscription state: useGateway reconciles
// what the tab follows from wantedSubscriptions on every store change, and a
// second owner here would send the same subscribe and unsubscribe frames
// twice and could disagree with it across a remount.
export interface SidebarContainerProps {
  client: ApiClient
  store: Store<AppState> & AppStore
}

export default function SidebarContainer({ client, store }: SidebarContainerProps) {
  const sessions = useStore(store, (state) => state.sessions)
  const selected = useStore(store, (state) => state.selectedSessionId)
  const workspace = useStore(store, (state) => state.bootstrap?.working_dir ?? '')
  const branch = useStore(store, (state) => state.bootstrap?.branch ?? '')
  const [error, setError] = useState<string | null>(null)
  const actions = useMemo(() => sessionActions(client), [client])

  // The unread markers come from a derived string, so the list re-renders
  // exactly when a marker changes and not on every frame. isUnread is the
  // one place the seen comparison lives; the component only asks it.
  const unreadIDs = useStore(store, (state) =>
    state.sessions
      .filter((row) => isUnread(state, row.id))
      .map((row) => row.id)
      .join(','),
  )
  const unread = useMemo(() => new Set(unreadIDs === '' ? [] : unreadIDs.split(',')), [unreadIDs])

  const workspaceName = workspace === '' ? 'Crush' : workspace.split('/').filter(Boolean).pop() ?? workspace

  const select = useCallback(
    (id: string) => {
      // Selecting only records the choice. useGateway subscribes the new
      // session and drops the old one when it is idle.
      store.selectSession(id)
    },
    [store],
  )

  const create = useCallback(() => {
    void actions
      .create()
      .then((row) => {
        // The store accepts the subscription snapshot that carries the new
        // session entity, so selecting it before the row arrives is safe.
        setError(null)
        select(row.id)
      })
      .catch((cause: unknown) => setError(message(cause)))
  }, [actions, select])

  const rename = useCallback(
    (id: string, title: string) => {
      void actions
        .rename(id, title)
        .then(() => setError(null))
        .catch((cause: unknown) => setError(message(cause)))
    },
    [actions],
  )

  const remove = useCallback(
    (id: string) => {
      void actions
        .remove(id)
        .then(() => {
          setError(null)
          // The deleted row, its messages and a selection pointing at it go
          // at once; the gateway's deleted frame would arrive later.
          store.forgetSession(id)
        })
        .catch((cause: unknown) => setError(message(cause)))
    },
    [actions, store],
  )

  const copyId = useCallback((id: string) => {
    void navigator.clipboard?.writeText(id)
  }, [])

  const openInTerminal = useCallback(
    (id: string) => {
      void actions
        .follow(id)
        .then(() => setError(null))
        .catch((cause: unknown) => setError(message(cause)))
    },
    [actions],
  )

  return (
    <Sidebar
      workspaceName={workspaceName}
      branch={branch}
      sessions={sessions}
      selectedSessionId={selected}
      unread={(id) => unread.has(id)}
      error={error}
      onSelect={select}
      onCreate={create}
      onRename={rename}
      onDelete={remove}
      onCopyId={copyId}
      onOpenInTerminal={openInTerminal}
    />
  )
}

// message is the gateway's own sentence for a refused request. The token and
// the request never reach it.
function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

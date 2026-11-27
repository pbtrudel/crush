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

  // Both separators, so a Windows path names its last directory too.
  const workspaceName = workspace === '' ? 'Crush' : workspace.split(/[\\/]/).filter(Boolean).pop() ?? workspace

  const dismissError = useCallback(() => setError(null), [])

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
      .then((session) => {
        // The store accepts the subscription snapshot that carries the new
        // session entity, so selecting it before the row arrives is safe.
        setError(null)
        select(session.id)
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

  // remove answers a promise the row waits on, so its delete button stays
  // disabled until the gateway has answered. It never rejects: the refusal
  // becomes the sidebar's error row.
  const remove = useCallback(
    async (id: string) => {
      try {
        await actions.remove(id)
      } catch (cause: unknown) {
        setError(message(cause))
        return
      }
      setError(null)
      // The deleted row, its messages and a selection pointing at it go at
      // once; the gateway's deleted frame would arrive later.
      store.forgetSession(id)
    },
    [actions, store],
  )

  // copyId reports a refused or unavailable clipboard instead of dropping the
  // promise: a silent click looks like a copied ID.
  const copyId = useCallback((id: string) => {
    const clipboard: Clipboard | undefined = navigator.clipboard
    if (clipboard === undefined) {
      setError(clipboardError)
      return
    }
    void clipboard.writeText(id).then(
      () => setError(null),
      () => setError(clipboardError),
    )
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
      onDismissError={dismissError}
    />
  )
}

// clipboardError is the one sentence for every way a copy can fail, so a
// browser without the API and one that refuses the write look the same.
const clipboardError = 'This browser will not let the tab copy the session ID'

// message is the gateway's own sentence for a refused request. The token and
// the request never reach it.
function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

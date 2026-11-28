import type {
  Bootstrap,
  MessageEvent,
  ParsedFrame,
  SessionEvent,
  SessionRow,
  SessionSnapshot,
  SessionStatusPayload,
  WebError,
  WireMessage,
} from '../api/wire'
import type { ConnectionState } from '../api/stream'
import { createStore, type Store } from './store'
import { readSeen, writeSeen } from './seen'

// WireMessage is the message shape the wire schemas answer with: the store
// keeps it typed rather than restating the fields.
export type { WireMessage }

export interface SessionData {
  messages: WireMessage[]
  hasMore: boolean
  queued: string[]
  files: { path: string }[]
  // snapshotSeq counts the snapshots the tab has applied for the session.
  // A snapshot replaces the messages, so history paged in against an older
  // one belongs to a list that is gone; the conversation reads this to drop
  // it.
  snapshotSeq: number
}

export interface AppState {
  connection: ConnectionState
  bootstrap: Bootstrap | null
  sessions: SessionRow[]
  selectedSessionId: string | null
  sessionData: Record<string, SessionData>
  lastSeen: Record<string, number>
  // error is the last web_error frame the gateway sent. The frame is global,
  // so session_id names the session it concerns and an empty one concerns
  // the whole tab.
  error: WebError | null
  // gatewayClosing records the gateway_closing frame: Crush is shutting down
  // and no reconnect can succeed until the terminal hands out a new link.
  gatewayClosing: boolean
}

export const emptySessionData: SessionData = {
  messages: [],
  hasMore: false,
  queued: [],
  files: [],
  snapshotSeq: 0,
}

export function createAppStore(storage: Storage = localStorage): Store<AppState> & AppStore {
  const store = createStore<AppState>({
    connection: 'connecting',
    bootstrap: null,
    sessions: [],
    selectedSessionId: null,
    sessionData: {},
    lastSeen: readSeen(storage),
    error: null,
    gatewayClosing: false,
  })
  // persistSeen writes lastSeen through after an action. Only a new reference
  // is durable: markSeen and pruneSeen hand back their input when they
  // changed nothing, so an action that moved no seen mark writes nothing.
  const persistSeen = (before: Record<string, number>) => {
    const after = store.get().lastSeen
    if (after !== before) {
      writeSeen(after, storage)
    }
  }
  return Object.assign(store, {
    applyBootstrap: (payload: Bootstrap) => {
      const before = store.get().lastSeen
      store.set((state) => reduceBootstrap(state, payload))
      persistSeen(before)
    },
    applyFrame: (frame: ParsedFrame) => {
      const before = store.get().lastSeen
      store.set((state) => reduceFrame(state, frame))
      persistSeen(before)
    },
    setConnection: (connection: ConnectionState) => store.set({ connection }),
    dismissError: () => store.set({ error: null }),
    selectSession: (sessionID: string) => {
      const before = store.get().lastSeen
      store.set((state) => ({
        selectedSessionId: sessionID,
        lastSeen: markSeen(state.lastSeen, sessionID, updatedAtOf(state, sessionID)),
        error: errorForSelection(state.error, sessionID),
      }))
      persistSeen(before)
    },
    // forgetSession drops a deleted session at once, without waiting for the
    // gateway's deleted frame: the row, its messages, a selection pointing at
    // it and its seen mark all go.
    forgetSession: (sessionID: string) => {
      const before = store.get().lastSeen
      store.set((state) => dropSession(state, sessionID))
      persistSeen(before)
    },
  })
}

export interface AppStore {
  applyBootstrap(payload: Bootstrap): void
  applyFrame(frame: ParsedFrame): void
  setConnection(connection: ConnectionState): void
  selectSession(sessionID: string): void
  forgetSession(sessionID: string): void
  dismissError(): void
}

export function reduceBootstrap(state: AppState, payload: Bootstrap): AppState {
  // A selection the gateway no longer lists is gone: keeping it would leave
  // the tab pointed at a session that cannot be subscribed to or rendered.
  const current = state.selectedSessionId
  const stillListed = current !== null && payload.sessions.some((row) => row.id === current)
  // || and not ??: current_session_id is "" when no terminal is attached,
  // and an empty string must fall through to the first row.
  const selectedSessionId = stillListed ? current : payload.current_session_id || payload.sessions[0]?.id || null
  // An empty list means ListSessions failed, not that every session vanished:
  // pruning then would throw away every seen mark the person has.
  const pruned = payload.sessions.length === 0 ? state.lastSeen : pruneSeen(state.lastSeen, payload.sessions)
  return {
    ...state,
    bootstrap: payload,
    sessions: payload.sessions,
    selectedSessionId,
    // The session a bootstrap opens was on screen before it was selected, so
    // it starts seen instead of unread. Without this, switching away from it
    // would show a marker for a session the person has been reading.
    lastSeen:
      selectedSessionId === null ? pruned : markSeen(pruned, selectedSessionId, updatedAtIn(payload.sessions, selectedSessionId)),
    // A bootstrap is a live gateway: the error from the old connection and
    // the closing mark it left behind are both stale now.
    error: null,
    gatewayClosing: false,
  }
}

export function reduceFrame(state: AppState, frame: ParsedFrame): AppState {
  switch (frame.type) {
    case 'session_snapshot': {
      const snapshot = frame.payload as SessionSnapshot
      const id = snapshot.session_id
      let sessions = state.sessions
      if (!hasSession(state, id)) {
        // A subscribe snapshot can beat the session created frame. When it
        // carries a non-empty entity the row comes from it; without one this
        // is a snapshot the tab cannot place and so must not keep.
        const entity = snapshot.session
        if (entity === undefined || entity.id === '') {
          return state
        }
        sessions = mergeSessionEvent(state.sessions, { type: 'created', payload: { ...entity, id } })
      }
      return {
        ...state,
        sessions,
        sessionData: {
          ...state.sessionData,
          [id]: {
            messages: snapshot.messages,
            hasMore: snapshot.has_more,
            queued: snapshot.queued,
            files: snapshot.files,
            snapshotSeq: (state.sessionData[id]?.snapshotSeq ?? 0) + 1,
          },
        },
      }
    }
    case 'session_status': {
      const payload = frame.payload as SessionStatusPayload
      return {
        ...state,
        sessions: state.sessions.map((row) => (row.id === payload.session_id ? { ...row, status: payload.status } : row)),
      }
    }
    case 'session': {
      // A created or renamed session arrives as a full entity; a deleted one
      // leaves the list. The selected session is marked seen at the
      // updated_at the entity carries, which is how a tab that is open on a
      // session never shows it unread.
      const event = frame.payload as SessionEvent
      const id = typeof event.payload.id === 'string' ? event.payload.id : ''
      if (id === '') {
        return state
      }
      // A session deleted in another tab leaves this one the same way a
      // local delete does: row, messages, selection and seen mark all go.
      if (event.type === 'deleted') {
        return dropSession(state, id)
      }
      const sessions = mergeSessionEvent(state.sessions, event)
      const lastSeen =
        id === state.selectedSessionId ? markSeen(state.lastSeen, id, updatedAtIn(sessions, id)) : state.lastSeen
      return { ...state, sessions, lastSeen }
    }
    case 'message': {
      // A deleted message leaves the list; a created or updated one is
      // upserted by id, which is idempotent for a frame a snapshot echoed.
      const envelope = frame.payload as MessageEvent
      const id = envelope.payload.session_id
      if (!hasSession(state, id)) {
        return state
      }
      const current = state.sessionData[id] ?? emptySessionData
      const messages =
        envelope.type === 'deleted'
          ? current.messages.filter((message) => message.id !== envelope.payload.id)
          : upsertMessage(current.messages, envelope.payload)
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, messages } },
      }
    }
    case 'web_error': {
      return { ...state, error: frame.payload as WebError }
    }
    case 'gateway_closing': {
      // The gateway is shutting down on purpose. The client keeps retrying
      // until its token is refused, but the tab can say at once that the
      // terminal is gone.
      return { ...state, gatewayClosing: true }
    }
    default: {
      return state
    }
  }
}

// upsertMessage writes a message by id. The search runs from the end: a log
// that holds an id twice shows its last copy, so that is the one an update
// has to reach.
function upsertMessage(messages: WireMessage[], next: WireMessage): WireMessage[] {
  const index = messages.findLastIndex((message) => message.id === next.id)
  if (index < 0) {
    return [...messages, next]
  }
  const copy = messages.slice()
  copy[index] = next
  return copy
}

function mergeSessionEvent(rows: SessionRow[], event: SessionEvent): SessionRow[] {
  const entity = event.payload as Record<string, unknown>
  const id = typeof entity.id === 'string' ? entity.id : ''
  if (!id) {
    return rows
  }
  const existing = rows.find((row) => row.id === id)
  const fallback: SessionRow = existing ?? {
    id,
    parent_session_id: '',
    title: '',
    message_count: 0,
    updated_at: 0,
    status: 'idle',
  }
  const merged = { ...fallback, ...entity, status: existing?.status ?? 'idle' } as SessionRow
  const next = rows.filter((row) => row.id !== id).concat(merged)
  // Newest updated first, the ID breaking ties, the gateway's order.
  next.sort((a, b) => b.updated_at - a.updated_at || a.id.localeCompare(b.id))
  return next
}

// dropSession is the one cleanup for a session that is gone, whether the
// tab deleted it or another tab did: the row, its messages, a selection
// pointing at it and its seen mark. The guards in reduceFrame then ignore a
// later snapshot, message or run end for it, so no orphan data comes back.
function dropSession(state: AppState, sessionID: string): AppState {
  const sessionData = { ...state.sessionData }
  delete sessionData[sessionID]
  const selectedSessionId = state.selectedSessionId === sessionID ? null : state.selectedSessionId
  return {
    ...state,
    sessions: state.sessions.filter((row) => row.id !== sessionID),
    sessionData,
    selectedSessionId,
    lastSeen: withoutKey(state.lastSeen, sessionID),
    error: errorForSelection(state.error, selectedSessionId),
  }
}

// withoutKey drops one entry, keeping the input when the key was absent so a
// caller can tell "changed" from "already gone" by the reference.
function withoutKey(seen: Record<string, number>, sessionID: string): Record<string, number> {
  if (!(sessionID in seen)) {
    return seen
  }
  const next: Record<string, number> = {}
  for (const [id, value] of Object.entries(seen)) {
    if (id !== sessionID) {
      next[id] = value
    }
  }
  return next
}

// pruneSeen drops seen marks for sessions the bootstrap no longer lists. The
// map otherwise grows with every session the workspace ever had.
function pruneSeen(seen: Record<string, number>, rows: SessionRow[]): Record<string, number> {
  const ids = new Set(rows.map((row) => row.id))
  const stale = Object.keys(seen).filter((id) => !ids.has(id))
  if (stale.length === 0) {
    return seen
  }
  const next: Record<string, number> = {}
  for (const [id, value] of Object.entries(seen)) {
    if (ids.has(id)) {
      next[id] = value
    }
  }
  return next
}

function hasSession(state: AppState, sessionID: string): boolean {
  return state.sessions.some((row) => row.id === sessionID)
}

// markSeen records the updated_at the person has seen for a session.
function markSeen(seen: Record<string, number>, sessionID: string, updatedAt: number): Record<string, number> {
  if ((seen[sessionID] ?? 0) >= updatedAt) {
    return seen
  }
  return { ...seen, [sessionID]: updatedAt }
}

function updatedAtIn(rows: SessionRow[], sessionID: string): number {
  return rows.find((row) => row.id === sessionID)?.updated_at ?? 0
}

function updatedAtOf(state: AppState, sessionID: string): number {
  return updatedAtIn(state.sessions, sessionID)
}

// isUnreadRow reports whether a session changed since the person last looked
// at it. The comparison is on the row's updated_at, which the workspace owns:
// it survives a Crush restart and needs no background subscription.
export function isUnreadRow(state: AppState, row: SessionRow): boolean {
  if (state.selectedSessionId === row.id) {
    return false
  }
  return row.updated_at > (state.lastSeen[row.id] ?? 0)
}

// isUnread is isUnreadRow for a caller that holds an id and not the row. The
// list asks the row version, so marking a list of sessions costs one pass.
export function isUnread(state: AppState, sessionID: string): boolean {
  const row = state.sessions.find((entry) => entry.id === sessionID)
  return row !== undefined && isUnreadRow(state, row)
}

export type ConnectionNotice = 'none' | 'reconnecting' | 'ended'

// connectionNotice is what the connection banner says. A stream that stopped
// after the shell rendered is over, and so is one whose gateway announced it
// is closing; a reconnect in progress is the only case that may recover.
export function connectionNotice(state: AppState): ConnectionNotice {
  if (state.gatewayClosing || state.connection === 'closed') {
    return 'ended'
  }
  if (state.connection === 'reconnecting') {
    return 'reconnecting'
  }
  return 'none'
}

// errorForSelection drops an error that names a session the tab is no longer
// on, so a stale message neither shows up later nor comes back when the
// selection returns. An error with an empty session_id is global and stays.
function errorForSelection(error: WebError | null, selectedSessionId: string | null): WebError | null {
  if (error === null || error.session_id === '') {
    return error
  }
  return error.session_id === selectedSessionId ? error : null
}

// visibleError answers the error the tab should show for the session on
// screen. A global error (an empty session_id) belongs to the whole tab and
// shows whichever session is selected; one that names a session shows only
// while that session is selected.
export function visibleError(state: AppState): WebError | null {
  if (state.error === null) {
    return null
  }
  if (state.error.session_id === '' || state.error.session_id === state.selectedSessionId) {
    return state.error
  }
  return null
}

// maxSubscriptions mirrors the gateway's per-connection cap.
export const maxSubscriptions = 32

// wantedSubscriptions is the set a tab follows: the selected session first,
// then every session that is not idle in session-list order, capped at the
// gateway's limit. The status comes from the global frames, so this needs no
// extra traffic to compute and only controls which session streams the tab
// receives.
export function wantedSubscriptions(state: AppState): string[] {
  const wanted = new Set<string>()
  if (state.selectedSessionId !== null) {
    wanted.add(state.selectedSessionId)
  }
  for (const row of state.sessions) {
    if (row.status !== 'idle') {
      wanted.add(row.id)
    }
  }
  return [...wanted].slice(0, maxSubscriptions)
}

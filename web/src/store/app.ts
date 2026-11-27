import type { Bootstrap, ParsedFrame, SessionRow, SessionStatus } from '../api/wire'
import type { ConnectionState } from '../api/stream'
import { createStore, type Store } from './store'
import { readSeen, writeSeen } from './seen'

export interface WireMessage {
  id: string
  role: string
  session_id: string
  parts: { type: string; data: unknown }[]
  created_at: number
  updated_at: number
}

export interface SessionData {
  messages: WireMessage[]
  hasMore: boolean
  busy: boolean
  queued: string[]
  files: { path: string }[]
}

export interface AppState {
  connection: ConnectionState
  bootstrap: Bootstrap | null
  sessions: SessionRow[]
  selectedSessionId: string | null
  sessionData: Record<string, SessionData>
  lastSeen: Record<string, number>
}

export const emptySessionData: SessionData = {
  messages: [],
  hasMore: false,
  busy: false,
  queued: [],
  files: [],
}

export function createAppStore(storage: Storage = localStorage): Store<AppState> & AppStore {
  const store = createStore<AppState>({
    connection: 'connecting',
    bootstrap: null,
    sessions: [],
    selectedSessionId: null,
    sessionData: {},
    lastSeen: readSeen(storage),
  })
  return Object.assign(store, {
    applyBootstrap: (payload: Bootstrap) => store.set((state) => reduceBootstrap(state, payload)),
    applyFrame: (frame: ParsedFrame) => store.set((state) => reduceFrame(state, frame)),
    setConnection: (connection: ConnectionState) => store.set({ connection }),
    selectSession: (sessionID: string) => {
      store.set((state) => ({
        selectedSessionId: sessionID,
        lastSeen: markSeen(state.lastSeen, sessionID, updatedAtOf(state, sessionID)),
      }))
      writeSeen(store.get().lastSeen, storage)
    },
    // forgetSession drops a deleted session at once, without waiting for the
    // gateway's deleted frame: the row, its messages and a selection pointing
    // at it all go.
    forgetSession: (sessionID: string) => {
      store.set((state) => {
        const sessionData = { ...state.sessionData }
        delete sessionData[sessionID]
        return {
          sessions: state.sessions.filter((row) => row.id !== sessionID),
          sessionData,
          selectedSessionId: state.selectedSessionId === sessionID ? null : state.selectedSessionId,
        }
      })
    },
  })
}

export interface AppStore {
  applyBootstrap(payload: Bootstrap): void
  applyFrame(frame: ParsedFrame): void
  setConnection(connection: ConnectionState): void
  selectSession(sessionID: string): void
  forgetSession(sessionID: string): void
}

export function reduceBootstrap(state: AppState, payload: Bootstrap): AppState {
  return {
    ...state,
    bootstrap: payload,
    sessions: payload.sessions,
    // || and not ??: current_session_id is "" when no terminal is attached,
    // and an empty string must fall through to the first row.
    selectedSessionId: state.selectedSessionId || payload.current_session_id || payload.sessions[0]?.id || null,
  }
}

export function reduceFrame(state: AppState, frame: ParsedFrame): AppState {
  switch (frame.type) {
    case 'session_snapshot': {
      const snapshot = frame.payload as {
        session_id: string
        messages: WireMessage[]
        has_more: boolean
        busy: boolean
        queued: string[]
        files: { path: string }[]
      }
      const id = snapshot.session_id
      return {
        ...state,
        sessionData: {
          ...state.sessionData,
          [id]: {
            messages: snapshot.messages,
            hasMore: snapshot.has_more,
            busy: snapshot.busy,
            queued: snapshot.queued,
            files: snapshot.files,
          },
        },
      }
    }
    case 'session_status': {
      const payload = frame.payload as { session_id: string; status: SessionStatus }
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
      const sessions = mergeSessionEvent(state.sessions, frame.payload)
      const id = sessionIDOf(frame.payload)
      const lastSeen =
        id !== null && id === state.selectedSessionId
          ? markSeen(state.lastSeen, id, updatedAtIn(sessions, id))
          : state.lastSeen
      return { ...state, sessions, lastSeen }
    }
    case 'message': {
      // A deleted message leaves the list; a created or updated one is
      // upserted by id, which is idempotent for a frame a snapshot echoed.
      const envelope = frame.payload as { type: string; payload: WireMessage }
      const id = envelope.payload.session_id
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
    case 'run_complete': {
      const payload = frame.payload as { type: string; payload: { session_id: string } }
      const id = payload.payload.session_id
      const current = state.sessionData[id] ?? emptySessionData
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, busy: false } },
      }
    }
    default: {
      return state
    }
  }
}

function upsertMessage(messages: WireMessage[], next: WireMessage): WireMessage[] {
  const index = messages.findIndex((message) => message.id === next.id)
  if (index < 0) {
    return [...messages, next]
  }
  const copy = messages.slice()
  copy[index] = next
  return copy
}

function mergeSessionEvent(rows: SessionRow[], payload: unknown): SessionRow[] {
  const envelope = payload as { type: string; payload?: Record<string, unknown> }
  const entity = envelope.payload
  const id = typeof entity?.id === 'string' ? entity.id : ''
  if (!id) {
    return rows
  }
  if (envelope.type === 'deleted') {
    return rows.filter((row) => row.id !== id)
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

// markSeen records the updated_at the person has seen for a session.
function markSeen(seen: Record<string, number>, sessionID: string, updatedAt: number): Record<string, number> {
  if ((seen[sessionID] ?? 0) >= updatedAt) {
    return seen
  }
  return { ...seen, [sessionID]: updatedAt }
}

function sessionIDOf(payload: unknown): string | null {
  const envelope = payload as { payload?: Record<string, unknown> }
  const id = envelope.payload?.id
  return typeof id === 'string' ? id : null
}

function updatedAtIn(rows: SessionRow[], sessionID: string): number {
  return rows.find((row) => row.id === sessionID)?.updated_at ?? 0
}

function updatedAtOf(state: AppState, sessionID: string): number {
  return updatedAtIn(state.sessions, sessionID)
}

// isUnread reports whether a session changed since the person last looked at
// it. The comparison is on the row's updated_at, which the workspace owns: it
// survives a Crush restart and needs no background subscription.
export function isUnread(state: AppState, sessionID: string): boolean {
  if (state.selectedSessionId === sessionID) {
    return false
  }
  return updatedAtIn(state.sessions, sessionID) > (state.lastSeen[sessionID] ?? 0)
}

// maxSubscriptions mirrors the gateway's per-connection cap.
export const maxSubscriptions = 32

// wantedSubscriptions is the set a tab follows: the selected session plus
// every session that is not idle, newest first, capped like the gateway does.
// The status comes from the global frames, so this needs no extra traffic to
// compute and only controls which session streams the tab receives.
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

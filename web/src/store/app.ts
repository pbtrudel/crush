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
  // older holds the pages read before the snapshot, oldest first. Live
  // edits and deletes reach them the same way they reach messages.
  older: WireMessage[]
  // hasMore says whether history exists before the oldest message held,
  // whichever list it is in.
  hasMore: boolean
  loadingOlder: boolean
  olderError: string | null
  queued: string[]
  files: { path: string }[]
  // snapshotSeq counts the snapshots the tab has applied for the session.
  snapshotSeq: number
  // generation is the page generation this entry's list belongs to, taken
  // from AppState.generation. An older page is applied only under the
  // generation its request started in, so a page that answers across a
  // resync, a new snapshot, or an entry the store dropped and opened again
  // is dropped instead of joining the new list.
  generation: number
}

export interface AppState {
  connection: ConnectionState
  bootstrap: Bootstrap | null
  sessions: SessionRow[]
  selectedSessionId: string | null
  sessionData: Record<string, SessionData>
  // generation is the tab's page counter. It only ever grows, and every
  // snapshot and every bootstrap reset takes the next value from it. Taking
  // one value per page set is what keeps a generation unique for the
  // session's lifetime: an entry deleted and opened again gets a value no
  // ticket handed out before the delete can name.
  generation: number
  // watchedChildren are the subagent sessions an open tool row follows.
  // They are subscribed after the live sessions and never get a row.
  watchedChildren: string[]
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
  older: [],
  hasMore: false,
  loadingOlder: false,
  olderError: null,
  queued: [],
  files: [],
  snapshotSeq: 0,
  generation: 0,
}

// noMessages is the one empty list, so an empty page keeps its identity.
const noMessages: WireMessage[] = []

export interface OlderPageTicket {
  sessionId: string
  generation: number
  before: string | null
}

// MessagesPage is what GET /api/sessions/{sid}/messages answers.
export interface MessagesPage {
  messages: WireMessage[]
  has_more: boolean
}

// isChildSession reports a subagent's session: the workspace lists only
// top-level sessions, and a child that slips in through a frame must not
// become a sidebar row.
export function isChildSession(row: { parent_session_id?: string }): boolean {
  return (row.parent_session_id ?? '') !== ''
}

export function createAppStore(storage: Storage = localStorage): Store<AppState> & AppStore {
  const store = createStore<AppState>({
    connection: 'connecting',
    bootstrap: null,
    sessions: [],
    selectedSessionId: null,
    sessionData: {},
    generation: 0,
    watchedChildren: [],
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
        // The open rows of the previous session are gone with it.
        watchedChildren: [],
        lastSeen: markSeen(state.lastSeen, sessionID, updatedAtOf(state, sessionID)),
        error: errorForSelection(state.error, sessionID),
      }))
      persistSeen(before)
    },
    // releaseSession drops what the tab loaded for a session it no longer
    // follows. The row stays: the session still exists, the tab just holds no
    // page of its history any more. The open session keeps its data — it is
    // always wanted while it is selected, so the guard is a second belt on
    // the invariant the frame renders from.
    releaseSession: (sessionID: string) => {
      store.set((state) => {
        if (sessionID === state.selectedSessionId || !(sessionID in state.sessionData)) {
          return {}
        }
        const sessionData = { ...state.sessionData }
        delete sessionData[sessionID]
        return { sessionData }
      })
    },
    // forgetSession drops a deleted session at once, without waiting for the
    // gateway's deleted frame: the row, its messages, a selection pointing at
    // it and its seen mark all go.
    forgetSession: (sessionID: string) => {
      const before = store.get().lastSeen
      store.set((state) => dropSession(state, sessionID))
      persistSeen(before)
    },
    beginOlderPage: (sessionID: string): OlderPageTicket | null => {
      const data = store.get().sessionData[sessionID]
      if (data === undefined || data.loadingOlder) {
        return null
      }
      const oldest = data.older[0] ?? data.messages[0]
      const ticket = { sessionId: sessionID, generation: data.generation, before: oldest?.id ?? null }
      store.set((state) => patchSession(state, sessionID, { loadingOlder: true, olderError: null }))
      return ticket
    },
    applyOlderPage: (ticket: OlderPageTicket, page: MessagesPage) => {
      store.set((state) => reduceOlderPage(state, ticket, page))
    },
    failOlderPage: (ticket: OlderPageTicket, error: string) => {
      store.set((state) => {
        const data = state.sessionData[ticket.sessionId]
        if (data === undefined || data.generation !== ticket.generation) {
          return {}
        }
        return patchSession(state, ticket.sessionId, { loadingOlder: false, olderError: error })
      })
    },
    watchChild: (sessionID: string) => {
      store.set((state) =>
        state.watchedChildren.includes(sessionID) ? {} : { watchedChildren: [...state.watchedChildren, sessionID] },
      )
    },
    // unwatchChild also drops the child's messages: nobody draws them, and
    // the next watch subscribes afresh and gets a new snapshot.
    unwatchChild: (sessionID: string) => {
      store.set((state) => {
        if (!state.watchedChildren.includes(sessionID)) {
          return {}
        }
        const sessionData = { ...state.sessionData }
        delete sessionData[sessionID]
        return { watchedChildren: state.watchedChildren.filter((id) => id !== sessionID), sessionData }
      })
    },
  })
}

export interface AppStore {
  applyBootstrap(payload: Bootstrap): void
  applyFrame(frame: ParsedFrame): void
  setConnection(connection: ConnectionState): void
  selectSession(sessionID: string): void
  releaseSession(sessionID: string): void
  forgetSession(sessionID: string): void
  dismissError(): void
  beginOlderPage(sessionID: string): OlderPageTicket | null
  applyOlderPage(ticket: OlderPageTicket, page: MessagesPage): void
  failOlderPage(ticket: OlderPageTicket, error: string): void
  watchChild(sessionID: string): void
  unwatchChild(sessionID: string): void
}

export function reduceBootstrap(state: AppState, payload: Bootstrap): AppState {
  // A subagent's session is never a row: the workspace lists only the
  // top-level sessions the person can open.
  const rows = payload.sessions.filter((row) => !isChildSession(row))
  // A selection the gateway no longer lists is gone: keeping it would leave
  // the tab pointed at a session that cannot be subscribed to or rendered.
  const current = state.selectedSessionId
  const stillListed = current !== null && rows.some((row) => row.id === current)
  // || and not ??: current_session_id is "" when no terminal is attached,
  // and an empty string must fall through to the first row.
  const selectedSessionId = stillListed ? current : payload.current_session_id || rows[0]?.id || null
  // An empty list means ListSessions failed, not that every session vanished:
  // pruning then would throw away every seen mark the person has.
  const pruned = rows.length === 0 ? state.lastSeen : pruneSeen(state.lastSeen, rows)
  // One value for the whole bootstrap: every entry the tab keeps belongs to
  // the new generation from here on, and no ticket can name it.
  const generation = state.generation + 1
  return {
    ...state,
    bootstrap: payload,
    sessions: rows,
    selectedSessionId,
    generation,
    sessionData: resetPages(state.sessionData, generation),
    // The session a bootstrap opens was on screen before it was selected, so
    // it starts seen instead of unread. Without this, switching away from it
    // would show a marker for a session the person has been reading.
    lastSeen: selectedSessionId === null ? pruned : markSeen(pruned, selectedSessionId, updatedAtIn(rows, selectedSessionId)),
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
      const entity = snapshot.session
      let sessions = state.sessions
      if (entity !== undefined && isChildSession(entity)) {
        // A subagent's session opens only for the tool row that asked.
        if (!state.watchedChildren.includes(id)) {
          return state
        }
      } else if (!hasSession(state, id)) {
        // A subscribe snapshot can beat the session created frame. When it
        // carries a non-empty entity the row comes from it; without one this
        // is a snapshot the tab cannot place and so must not keep.
        if (entity === undefined || entity.id === '') {
          return state
        }
        sessions = mergeSessionEvent(state.sessions, { type: 'created', payload: { ...entity, id } })
      }
      const previous = state.sessionData[id]
      // The next value from the tab's counter, so a generation this session
      // held at any earlier point cannot come round again.
      const generation = state.generation + 1
      return {
        ...state,
        sessions,
        generation,
        sessionData: {
          ...state.sessionData,
          [id]: {
            messages: snapshot.messages,
            older: noMessages,
            hasMore: snapshot.has_more,
            loadingOlder: false,
            olderError: null,
            queued: snapshot.queued,
            files: snapshot.files,
            snapshotSeq: (previous?.snapshotSeq ?? 0) + 1,
            generation,
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
      // A subagent's session is never a row: its created and updated frames
      // are dropped, and a deleted one only clears what the tab held.
      if (event.type !== 'deleted' && isChildSession(event.payload)) {
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
      // Only a snapshot opens a session's data. A message counts only while
      // the tab holds that page: one the gateway had in flight when the tab
      // unsubscribed must not put back what releaseSession dropped, and one
      // that beats the snapshot is in the snapshot already. Data without a
      // row cannot exist — the snapshot that opens it needs the row — so this
      // also keeps frames for an unlisted session out.
      const current = state.sessionData[id]
      if (current === undefined) {
        return state
      }
      const next = envelope.payload
      if (envelope.type === 'deleted') {
        return {
          ...state,
          sessionData: {
            ...state.sessionData,
            [id]: {
              ...current,
              messages: current.messages.filter((entry) => entry.id !== next.id),
              older: current.older.filter((entry) => entry.id !== next.id),
            },
          },
        }
      }
      // An edit to a message that lives in a loaded older page is written
      // there; anything else is upserted into the live list.
      const olderIndex = current.older.findIndex((entry) => entry.id === next.id)
      if (olderIndex >= 0) {
        const older = current.older.slice()
        older[olderIndex] = next
        return { ...state, sessionData: { ...state.sessionData, [id]: { ...current, older } } }
      }
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, messages: upsertMessage(current.messages, next) } },
      }
    }
    case 'model_changed': {
      if (state.bootstrap === null) {
        return state
      }
      return { ...state, bootstrap: { ...state.bootstrap, model: frame.payload as Bootstrap['model'] } }
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

function patchSession(state: AppState, sessionID: string, patch: Partial<SessionData>): Partial<AppState> {
  const data = state.sessionData[sessionID]
  if (data === undefined) {
    return {}
  }
  return { sessionData: { ...state.sessionData, [sessionID]: { ...data, ...patch } } }
}

// reduceOlderPage puts an older page in front of what is loaded, unless the
// session's generation moved while the request was out.
export function reduceOlderPage(state: AppState, ticket: OlderPageTicket, page: MessagesPage): AppState {
  const data = state.sessionData[ticket.sessionId]
  if (data === undefined || data.generation !== ticket.generation) {
    return state
  }
  const held = new Set([...data.older, ...data.messages].map((entry) => entry.id))
  const fresh = page.messages.filter((entry) => !held.has(entry.id))
  return {
    ...state,
    sessionData: {
      ...state.sessionData,
      [ticket.sessionId]: {
        ...data,
        older: fresh.length === 0 ? data.older : [...fresh, ...data.older],
        hasMore: page.has_more,
        loadingOlder: false,
        olderError: null,
      },
    },
  }
}

// visibleMessages is the log a conversation shows: the loaded pages, then
// the snapshot and its live updates, each id once. A resync can echo a
// message the tab already paged in; the first copy wins.
export function visibleMessages(older: WireMessage[], messages: WireMessage[]): WireMessage[] {
  if (older.length === 0) {
    return messages
  }
  const seen = new Set<string>()
  const out: WireMessage[] = []
  for (const entry of [...older, ...messages]) {
    if (!seen.has(entry.id)) {
      seen.add(entry.id)
      out.push(entry)
    }
  }
  return out
}

// resetPages drops every loaded older page and moves every entry to the
// generation a bootstrap reset takes from the tab's counter: whatever was
// read under the old connection belongs to a list the new snapshots
// replace. Dropping a page means history exists before the snapshot, so
// has_more turns true.
function resetPages(sessionData: Record<string, SessionData>, generation: number): Record<string, SessionData> {
  const out: Record<string, SessionData> = {}
  for (const [id, data] of Object.entries(sessionData)) {
    out[id] = {
      ...data,
      older: noMessages,
      hasMore: data.older.length > 0 ? true : data.hasMore,
      loadingOlder: false,
      olderError: null,
      generation,
    }
  }
  return out
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
// tab deleted it or another tab did: the row, its messages, a watch on it,
// a selection pointing at it and its seen mark. The guards in reduceFrame
// then ignore a later snapshot, message or run end for it, so no orphan
// data comes back.
function dropSession(state: AppState, sessionID: string): AppState {
  const sessionData = { ...state.sessionData }
  delete sessionData[sessionID]
  const selectedSessionId = state.selectedSessionId === sessionID ? null : state.selectedSessionId
  return {
    ...state,
    sessions: state.sessions.filter((row) => row.id !== sessionID),
    sessionData,
    // A deleted subagent stops holding a subscription slot as well.
    watchedChildren: state.watchedChildren.includes(sessionID)
      ? state.watchedChildren.filter((id) => id !== sessionID)
      : state.watchedChildren,
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
  for (const sessionID of state.watchedChildren) {
    wanted.add(sessionID)
  }
  return [...wanted].slice(0, maxSubscriptions)
}

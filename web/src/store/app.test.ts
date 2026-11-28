import { describe, expect, it } from 'vitest'

import type { Bootstrap, ParsedFrame } from '../api/wire'
import { memoryStorage } from '../app/storage'
import {
  connectionNotice,
  createAppStore,
  isUnread,
  isUnreadRow,
  maxSubscriptions,
  reduceBootstrap,
  reduceFrame,
  visibleError,
  wantedSubscriptions,
  type AppState,
  type WireMessage,
} from './app'

const bootstrap: Bootstrap = {
  version: 'test',
  working_dir: '/repo',
  current_session_id: 's1',
  sessions: [
    { id: 's1', parent_session_id: '', title: 'Parser refactor', message_count: 2, updated_at: 20, status: 'idle' },
    { id: 's2', parent_session_id: '', title: 'Tests', message_count: 0, updated_at: 10, status: 'running' },
  ],
  model: { id: 'm', name: 'M', provider: 'p', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'normal',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
} as Bootstrap

function frame(type: string, payload: unknown, seq = 1, sessionId?: string): ParsedFrame {
  return { seq, type, payload, sessionId }
}

// snapshotOf applies the snapshot a subscribe answers with. Message frames
// only arrive behind it, so a test that streams a message seeds one first.
function snapshotOf(state: AppState, sessionID: string): AppState {
  return reduceFrame(
    state,
    frame('session_snapshot', { session_id: sessionID, messages: [], has_more: false, files: [], busy: false, queued: [] }, 0, sessionID),
  )
}

// messageFrame is one created or updated message in the given session.
function messageFrame(sessionID: string, id: string, type = 'created'): ParsedFrame {
  return frame(
    'message',
    { type, payload: { id, role: 'user', session_id: sessionID, parts: [], created_at: 1, updated_at: 1 } },
    2,
    sessionID,
  )
}

describe('reduceBootstrap', () => {
  it('fills the session list and the selected session', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    const state = store.get()
    expect(state.sessions.map((row) => row.id)).toEqual(['s1', 's2'])
    expect(state.selectedSessionId).toBe('s1')
    expect(state.bootstrap?.working_dir).toBe('/repo')
  })

  it('falls through an empty current session id to the first row', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), { ...bootstrap, current_session_id: '' })
    expect(start.selectedSessionId).toBe('s1')
  })

  it('clears the last web_error', () => {
    const store = createAppStore(memoryStorage())
    store.applyFrame(frame('web_error', { session_id: '', code: 'gateway', error: 'the gateway is busy' }, 7))
    expect(store.get().error).not.toBeNull()
    // A bootstrap is the gateway saying it is alive now: an error from the
    // previous connection must not outlive it.
    store.applyBootstrap(bootstrap)
    expect(store.get().error).toBeNull()
  })
})

describe('reduceFrame', () => {
  it('moves a session status without touching the list order', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(start, frame('session_status', { session_id: 's1', status: 'running' }, 5))
    expect(next.sessions[0].status).toBe('running')
    expect(next.sessions.map((row) => row.id)).toEqual(['s1', 's2'])
  })

  it('upserts a message by id', () => {
    const start = snapshotOf(reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap), 's1')
    const first = reduceFrame(
      start,
      frame('message', { type: 'created', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 2, 's1'),
    )
    expect(first.sessionData.s1.messages).toHaveLength(1)

    const second = reduceFrame(
      first,
      frame('message', { type: 'updated', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 9 } }, 3, 's1'),
    )
    expect(second.sessionData.s1.messages).toHaveLength(1)
    expect(second.sessionData.s1.messages[0].updated_at).toBe(9)
  })

  it('rewrites the last copy of a message the log holds twice', () => {
    // Older pages are prepended in front of the snapshot, so one id can name
    // two entries. The upsert has to land on the copy the end of the list
    // shows, or a streamed update would change a message behind the fold.
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const copy: WireMessage = {
      id: 'm1',
      role: 'user',
      session_id: 's1',
      parts: [],
      created_at: 1,
      updated_at: 1,
    }
    const withCopies = {
      ...start,
      sessionData: {
        s1: { messages: [copy, copy], hasMore: false, queued: [], files: [], snapshotSeq: 0 },
      },
    }
    const next = reduceFrame(
      withCopies,
      frame('message', { type: 'updated', payload: { ...copy, updated_at: 9 } }, 3, 's1'),
    )
    expect(next.sessionData.s1.messages).toHaveLength(2)
    expect(next.sessionData.s1.messages[0].updated_at).toBe(1)
    expect(next.sessionData.s1.messages[1].updated_at).toBe(9)
  })

  it('upserts a session entity and keeps its status', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Renamed', message_count: 1, updated_at: 30 } }, 4),
    )
    expect(next.sessions[0].id).toBe('s2')
    expect(next.sessions[0].title).toBe('Renamed')
    expect(next.sessions[0].status).toBe('running')
  })

  it('drops a deleted session', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(start, frame('session', { type: 'deleted', payload: { id: 's2' } }, 5))
    expect(next.sessions.map((row) => row.id)).toEqual(['s1'])
  })

  it('replaces a session from its snapshot', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session_snapshot', {
        session_id: 's2',
        messages: [{ id: 'm2', role: 'user', session_id: 's2', parts: [], created_at: 1, updated_at: 1 }],
        has_more: true,
        files: [],
        busy: false,
        queued: ['later'],
      }, 6, 's2'),
    )
    expect(next.sessionData.s2.messages).toHaveLength(1)
    expect(next.sessionData.s2.hasMore).toBe(true)
    expect(next.sessionData.s2.queued).toEqual(['later'])
  })

  it('counts the snapshots it applies for a session', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const snapshot = frame('session_snapshot', {
      session_id: 's2',
      messages: [],
      has_more: false,
      files: [],
      busy: false,
      queued: [],
    }, 6, 's2')
    const once = reduceFrame(start, snapshot)
    const twice = reduceFrame(once, snapshot)
    expect(once.sessionData.s2.snapshotSeq).toBe(1)
    expect(twice.sessionData.s2.snapshotSeq).toBe(2)
  })

  it('drops a deleted message', () => {
    const start = snapshotOf(reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap), 's1')
    const withMessage = reduceFrame(
      start,
      frame('message', { type: 'created', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 2, 's1'),
    )
    const next = reduceFrame(
      withMessage,
      frame('message', { type: 'deleted', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 3, 's1'),
    )
    expect(next.sessionData.s1.messages).toHaveLength(0)
  })

  it('marks a session unread when its updated_at moves past what was seen', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const state = reduceFrame(
      start,
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 9),
    )
    expect(isUnread(state, 's2')).toBe(true)
    expect(isUnread(state, 's1'), 'the open session is never unread').toBe(false)
  })

  it('marks the open session seen when its own update arrives', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s1')
    store.applyFrame(frame('session', { type: 'updated', payload: { id: 's1', title: 'Parser refactor', message_count: 3, updated_at: 55 } }, 10))
    expect(isUnread(store.get(), 's1')).toBe(false)
  })

  it('records the seen updated_at a frame for the open session carries', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s1')
    store.applyFrame(frame('session', { type: 'updated', payload: { id: 's1', title: 'Parser refactor', message_count: 3, updated_at: 55 } }, 10))
    expect(store.get().lastSeen.s1).toBe(55)
  })
})

describe('frames for a session the tab released', () => {
  it('leaves no entry for a message that was queued behind the unsubscribe', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.applyFrame(
      frame('session_snapshot', {
        session_id: 's2',
        messages: [{ id: 's2-m1', role: 'user', session_id: 's2', parts: [], created_at: 1, updated_at: 1 }],
        has_more: false,
        files: [],
        busy: false,
        queued: [],
      }, 1, 's2'),
    )
    store.releaseSession('s2')
    const released = store.get()

    // The gateway had this frame in flight when the tab unsubscribed. It must
    // not put the page the tab just dropped back into the store.
    store.applyFrame(messageFrame('s2', 's2-m2'))
    expect(store.get().sessionData.s2).toBeUndefined()
    expect(store.get()).toBe(released)
  })

  it('still applies the first live message of the session on screen', () => {
    const selected = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    expect(selected.selectedSessionId).toBe('s1')
    const afterSnapshot = snapshotOf(selected, 's1')
    const next = reduceFrame(afterSnapshot, messageFrame('s1', 'm1'))
    expect(next.sessionData.s1.messages.map((message) => message.id)).toEqual(['m1'])
  })

  it('still applies a message to a session whose snapshot beat its created frame', () => {
    // A session created and selected in this tab: the subscribe snapshot is
    // the first frame that mentions it, and it carries the row.
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const created = reduceFrame(
      start,
      frame('session_snapshot', {
        session_id: 's4',
        session: { id: 's4', title: 'Fresh', message_count: 0, updated_at: 50 },
        messages: [],
        has_more: false,
        files: [],
        busy: false,
        queued: [],
      }, 1, 's4'),
    )
    expect(created.sessions.map((row) => row.id)).toEqual(['s4', 's1', 's2'])
    const next = reduceFrame(created, messageFrame('s4', 's4-m1'))
    expect(next.sessionData.s4.messages.map((message) => message.id)).toEqual(['s4-m1'])
  })
})

describe('run_complete frames', () => {
  it('leaves the store alone: the row status is the run state', () => {
    // The frame says a run ended, and the session_status frame beside it is
    // what the list shows. Nothing reads a second copy of that state, so the
    // reducer has nothing to do.
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const done = reduceFrame(start, frame('run_complete', { type: 'complete', payload: { session_id: 's1', message_id: 'm1' } }, 4, 's1'))
    expect(done).toBe(start)
  })
})

describe('isUnreadRow', () => {
  it('answers what isUnread answers, without looking the row up again', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const state = reduceFrame(
      start,
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 9),
    )
    for (const row of state.sessions) {
      expect(isUnreadRow(state, row), row.id).toBe(isUnread(state, row.id))
    }
    const moved = state.sessions.find((row) => row.id === 's2')
    expect(moved).toBeDefined()
    expect(isUnreadRow(state, moved!)).toBe(true)
    // The session on screen is being read, so it never carries the marker.
    const open = state.sessions.find((row) => row.id === 's1')
    expect(isUnreadRow({ ...state, selectedSessionId: 's1' }, open!)).toBe(false)
  })
})

describe('AppStore.selectSession', () => {
  it('marks the row seen at its updated_at and persists it', () => {
    const storage = memoryStorage()
    const store = createAppStore(storage)
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 9))
    expect(isUnread(store.get(), 's2')).toBe(true)
    store.selectSession('s2')
    expect(isUnread(store.get(), 's2')).toBe(false)
    expect(storage.getItem('crush.seen')).toContain('"s2":40')
  })
})

describe('AppStore.releaseSession', () => {
  function withMessages(store: ReturnType<typeof createAppStore>, sessionID: string) {
    store.applyFrame(
      frame('session_snapshot', {
        session_id: sessionID,
        messages: [{ id: `${sessionID}-m1`, role: 'user', session_id: sessionID, parts: [], created_at: 1, updated_at: 1 }],
        has_more: false,
        files: [],
        busy: false,
        queued: [],
      }, 2, sessionID),
    )
  }

  it('drops what the tab loaded for a session it stopped following', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    withMessages(store, 's2')
    expect(store.get().sessionData.s2).toBeDefined()
    store.releaseSession('s2')
    expect(store.get().sessionData.s2).toBeUndefined()
    // The row stays: the session still exists, the tab just holds no page of
    // its history any more.
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1', 's2'])
  })

  it('never releases the session on screen', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    withMessages(store, 's2')
    store.releaseSession('s2')
    expect(store.get().sessionData.s2?.messages).toHaveLength(1)
  })

  it('keeps the state object when there is nothing to release', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    const before = store.get()
    store.releaseSession('s2')
    expect(store.get()).toBe(before)
  })
})

describe('AppStore.forgetSession', () => {
  it('clears the selection and the messages of the deleted session', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    store.forgetSession('s2')
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1'])
    expect(store.get().selectedSessionId).toBeNull()
    expect(store.get().sessionData.s2).toBeUndefined()
  })

  it('drops the messages a snapshot had loaded for the deleted session', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    store.applyFrame(
      frame('session_snapshot', {
        session_id: 's2',
        messages: [{ id: 'm2', role: 'user', session_id: 's2', parts: [], created_at: 1, updated_at: 1 }],
        has_more: false,
        files: [],
        busy: false,
        queued: [],
      }, 6, 's2'),
    )
    expect(store.get().sessionData.s2.messages).toHaveLength(1)
    store.forgetSession('s2')
    expect(store.get().sessionData.s2).toBeUndefined()
  })
})

const threeSessions: Bootstrap = {
  ...bootstrap,
  sessions: [
    ...bootstrap.sessions,
    { id: 's3', parent_session_id: '', title: 'Docs', message_count: 0, updated_at: 10, status: 'idle' },
  ],
}

describe('unread persistence', () => {
  it('does not mark the session the bootstrap opened unread after switching away', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    expect(isUnread(store.get(), 's1')).toBe(false)
  })

  it('remembers a turn watched on a selected session across a reload', () => {
    const storage = memoryStorage()
    const first = createAppStore(storage)
    first.applyBootstrap(threeSessions)
    first.selectSession('s3')
    first.applyFrame(
      frame('session', { type: 'updated', payload: { id: 's3', title: 'Docs', message_count: 1, updated_at: 30 } }, 7),
    )
    expect(first.get().lastSeen.s3).toBe(30)

    const reloaded: Bootstrap = {
      ...threeSessions,
      current_session_id: 's1',
      sessions: threeSessions.sessions.map((row) => (row.id === 's3' ? { ...row, updated_at: 30 } : row)),
    }
    const second = createAppStore(storage)
    second.applyBootstrap(reloaded)
    expect(second.get().selectedSessionId).toBe('s1')
    expect(isUnread(second.get(), 's3')).toBe(false)
  })

  it('writes crush.seen only when a seen mark moves', () => {
    const storage = memoryStorage()
    const store = createAppStore(storage)
    store.applyBootstrap(bootstrap)
    store.applyFrame(
      frame('session_snapshot', { session_id: 's1', messages: [], has_more: false, files: [], busy: false, queued: [] }, 1, 's1'),
    )
    const afterBootstrap = storage.getItem('crush.seen')
    store.applyFrame(
      frame('message', { type: 'created', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 2, 's1'),
    )
    store.applyFrame(
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 3),
    )
    expect(storage.getItem('crush.seen')).toBe(afterBootstrap)
  })
})

describe('a session deleted by another tab', () => {
  it('forgets its messages, selection and seen mark', () => {
    const storage = memoryStorage()
    const store = createAppStore(storage)
    store.applyBootstrap(bootstrap)
    store.applyFrame(
      frame('session_snapshot', {
        session_id: 's2',
        messages: [{ id: 'm2', role: 'user', session_id: 's2', parts: [], created_at: 1, updated_at: 1 }],
        has_more: false,
        files: [],
        busy: false,
        queued: [],
      }, 2, 's2'),
    )
    store.selectSession('s2')
    store.applyFrame(frame('session', { type: 'deleted', payload: { id: 's2' } }, 3))
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1'])
    expect(store.get().selectedSessionId).toBeNull()
    expect(store.get().sessionData.s2).toBeUndefined()
    expect(store.get().lastSeen.s2).toBeUndefined()
    expect(storage.getItem('crush.seen')).not.toContain('s2')
  })

  it('keeps a selection the next bootstrap still lists', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    store.applyBootstrap(bootstrap)
    expect(store.get().selectedSessionId).toBe('s2')
  })

  it('moves a selection the next bootstrap no longer lists to a live one', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    store.applyBootstrap({ ...bootstrap, current_session_id: '', sessions: [bootstrap.sessions[0]] })
    expect(store.get().selectedSessionId).toBe('s1')
  })
})

describe('frames for a session the bootstrap does not list', () => {
  it('leaves no orphan session data', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const snapshot = reduceFrame(
      start,
      frame('session_snapshot', { session_id: 'gone', messages: [], has_more: false, files: [], busy: true, queued: [] }, 2, 'gone'),
    )
    expect(snapshot.sessionData.gone).toBeUndefined()
    const message = reduceFrame(
      start,
      frame('message', { type: 'created', payload: { id: 'm9', role: 'user', session_id: 'gone', parts: [], created_at: 1, updated_at: 1 } }, 3, 'gone'),
    )
    expect(message.sessionData.gone).toBeUndefined()
    const done = reduceFrame(start, frame('run_complete', { type: 'complete', payload: { session_id: 'gone', message_id: 'm9' } }, 4))
    expect(done.sessionData.gone).toBeUndefined()
    expect(done).toBe(start)
  })
})

describe('seen pruning', () => {
  it('drops seen entries for sessions a bootstrap no longer lists', () => {
    const storage = memoryStorage({ 'crush.seen': JSON.stringify({ s1: 20, gone: 99 }) })
    const store = createAppStore(storage)
    store.applyBootstrap(bootstrap)
    expect(store.get().lastSeen.gone).toBeUndefined()
    expect(store.get().lastSeen.s1).toBe(20)
    expect(storage.getItem('crush.seen')).not.toContain('gone')
  })

  it('keeps every seen mark when the gateway sends no sessions', () => {
    const seeded = { s1: 20, s2: 10 }
    const storage = memoryStorage({ 'crush.seen': JSON.stringify(seeded) })
    const store = createAppStore(storage)
    // ListSessions failed, so the gateway answers an empty list. That is not
    // proof that every session vanished, and the seen marks must survive it.
    store.applyBootstrap({ ...bootstrap, current_session_id: '', sessions: [] })
    expect(store.get().lastSeen).toEqual(seeded)
    expect(storage.getItem('crush.seen')).toBe(JSON.stringify(seeded))
  })
})

describe('a snapshot that beats the session created frame', () => {
  it('adds the row the snapshot carries', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session_snapshot', {
        session_id: 's4',
        session: { id: 's4', title: 'Fresh', message_count: 1, updated_at: 50 },
        messages: [{ id: 'm4', role: 'user', session_id: 's4', parts: [], created_at: 1, updated_at: 1 }],
        has_more: false,
        files: [],
        busy: true,
        queued: [],
      }, 2, 's4'),
    )
    expect(next.sessions.map((row) => row.id)).toEqual(['s4', 's1', 's2'])
    expect(next.sessions[0].title).toBe('Fresh')
    expect(next.sessions[0].status).toBe('idle')
    expect(next.sessionData.s4.messages).toHaveLength(1)
  })

  it('still drops a snapshot with no session entity', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session_snapshot', { session_id: 'gone', messages: [], has_more: false, files: [], busy: true, queued: [] }, 2, 'gone'),
    )
    expect(next).toBe(start)
    expect(next.sessionData.gone).toBeUndefined()
  })
})

describe('the connection notice', () => {
  function stateWith(overrides: Partial<ReturnType<typeof createAppStore>['get']> = {}) {
    return { ...createAppStore(memoryStorage()).get(), ...overrides }
  }

  it('says nothing while the stream is open', () => {
    expect(connectionNotice(stateWith({ connection: 'open' }))).toBe('none')
    expect(connectionNotice(stateWith({ connection: 'connecting' }))).toBe('none')
  })

  it('reports a reconnecting stream', () => {
    expect(connectionNotice(stateWith({ connection: 'reconnecting' }))).toBe('reconnecting')
  })

  it('reports the end once the stream has stopped', () => {
    expect(connectionNotice(stateWith({ connection: 'closed' }))).toBe('ended')
  })

  it('records that the gateway is closing, even while the client retries', () => {
    const store = createAppStore(memoryStorage())
    store.applyFrame(frame('gateway_closing', { reason: 'the terminal quit' }, 9))
    expect(store.get().gatewayClosing).toBe(true)
    store.setConnection('reconnecting')
    expect(connectionNotice(store.get())).toBe('ended')
  })

  it('reopens the notice on a bootstrap from a live gateway', () => {
    const store = createAppStore(memoryStorage())
    store.applyFrame(frame('gateway_closing', { reason: 'the terminal quit' }, 9))
    store.applyBootstrap(bootstrap)
    expect(store.get().gatewayClosing).toBe(false)
    expect(connectionNotice(store.get())).toBe('none')
  })
})

describe('AppStore.dismissError', () => {
  it('clears the last web_error', () => {
    const store = createAppStore(memoryStorage())
    store.applyFrame(frame('web_error', { session_id: '', code: 'gateway', error: 'the gateway is busy' }, 7))
    expect(visibleError(store.get())).not.toBeNull()
    store.dismissError()
    expect(store.get().error).toBeNull()
    expect(visibleError(store.get())).toBeNull()
  })
})

describe('wantedSubscriptions', () => {
  it('follows the open session and every non-idle session', () => {
    const state = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    // bootstrap: s1 idle and current, s2 running.
    expect(wantedSubscriptions(state)).toEqual(['s1', 's2'])
    const other = { ...state, selectedSessionId: 's2' }
    expect(wantedSubscriptions(other)).toEqual(['s2'])
  })

  it('caps the set at the gateway limit', () => {
    const rows = Array.from({ length: maxSubscriptions + 5 }, (_, index) => ({
      id: `s${index}`,
      parent_session_id: '',
      title: `t${index}`,
      message_count: 0,
      updated_at: index,
      status: 'running' as const,
    }))
    const state = { ...reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap), sessions: rows, selectedSessionId: null }
    expect(maxSubscriptions).toBe(32)
    expect(wantedSubscriptions(state)).toHaveLength(maxSubscriptions)
  })
})

describe('web_error frames', () => {
  it('keeps a global error whichever session is selected', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(start, frame('web_error', { session_id: '', code: 'gateway', error: 'the gateway is busy' }, 7))
    expect(visibleError(next)?.error).toBe('the gateway is busy')
    expect(visibleError({ ...next, selectedSessionId: 's2' })?.error).toBe('the gateway is busy')
  })

  it('shows an error that names a session only for that session', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(start, frame('web_error', { session_id: 's2', code: 'agent_busy', error: 'busy' }, 7))
    expect(visibleError(next)).toBeNull()
    expect(visibleError({ ...next, selectedSessionId: 's2' })?.error).toBe('busy')
  })

  it('has no error to show before one arrives', () => {
    expect(visibleError(createAppStore(memoryStorage()).get())).toBeNull()
  })

  it('drops an error that names a session once the selection moves on', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('web_error', { session_id: 's1', code: 'agent_busy', error: 'busy' }, 7))
    expect(store.get().error).not.toBeNull()
    store.selectSession('s2')
    expect(store.get().error).toBeNull()
  })

  it('keeps an error for the session that is still selected', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('web_error', { session_id: 's1', code: 'agent_busy', error: 'busy' }, 7))
    store.selectSession('s1')
    expect(visibleError(store.get())?.error).toBe('busy')
  })

  it('keeps a global error across a selection change', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('web_error', { session_id: '', code: 'gateway', error: 'the gateway is busy' }, 7))
    store.selectSession('s2')
    expect(visibleError(store.get())?.error).toBe('the gateway is busy')
  })

  it('drops a per-session error when a bootstrap moves the selection', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('web_error', { session_id: 's1', code: 'agent_busy', error: 'busy' }, 7))
    store.applyBootstrap({
      ...bootstrap,
      current_session_id: 's2',
      sessions: [bootstrap.sessions[1]],
    })
    expect(store.get().selectedSessionId).toBe('s2')
    expect(store.get().error).toBeNull()
  })
})

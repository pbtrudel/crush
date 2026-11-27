import { describe, expect, it } from 'vitest'

import type { Bootstrap, ParsedFrame } from '../api/wire'
import { memoryStorage } from '../test/memoryStorage'
import {
  createAppStore,
  isUnread,
  maxSubscriptions,
  reduceBootstrap,
  reduceFrame,
  wantedSubscriptions,
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
})

describe('reduceFrame', () => {
  it('moves a session status without touching the list order', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
    const next = reduceFrame(start, frame('session_status', { session_id: 's1', status: 'running' }, 5))
    expect(next.sessions[0].status).toBe('running')
    expect(next.sessions.map((row) => row.id)).toEqual(['s1', 's2'])
  })

  it('upserts a message by id', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
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

  it('drops a deleted message', () => {
    const start = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrap)
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

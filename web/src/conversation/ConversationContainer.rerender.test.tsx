import { act, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/http'
import type { ParsedFrame, SessionRow } from '../api/wire'
import { memoryStorage } from '../app/storage'
import { createAppStore, emptySessionData, type AppState, type WireMessage } from '../store/app'
import ConversationContainer from './ConversationContainer'

// The tool row stands in for everything a message draws: counting its renders
// is how this spec sees a row that drew again. It also records the tool
// environment it read, which is the value every row and every context
// consumer shares.
const counters = vi.hoisted(() => ({ rows: {} as Record<string, number>, environments: [] as unknown[] }))

vi.mock('../tools/ToolCall', async () => {
  const [{ useContext }, { ToolEnvironmentContext }] = await Promise.all([
    import('react'),
    import('../tools/environment'),
  ])
  function Row({ call }: { call: { id: string; name: string } }) {
    counters.rows[call.id] = (counters.rows[call.id] ?? 0) + 1
    counters.environments.push(useContext(ToolEnvironmentContext))
    return <li>{call.name}</li>
  }
  return { default: Row }
})

const session: SessionRow = {
  id: 's1',
  parent_session_id: '',
  title: 'Port the parser',
  message_count: 2,
  updated_at: 10,
  status: 'running',
}

function message(id: string, parts: WireMessage['parts']): WireMessage {
  return { id, role: 'assistant', session_id: 's1', parts, created_at: 1, updated_at: 1 }
}

function toolCall(id: string, name: string): WireMessage['parts'][number] {
  return { type: 'tool_call', data: { id, name, input: '{"command":"ls"}' } }
}

function text(value: string): WireMessage['parts'][number] {
  return { type: 'text', data: { text: value } }
}

function delta(next: WireMessage): ParsedFrame {
  return { seq: 2, type: 'message', payload: { type: 'updated', payload: next } }
}

function setup(messages: WireMessage[]) {
  const store = createAppStore(memoryStorage())
  const state: Partial<AppState> = {
    sessions: [session],
    selectedSessionId: 's1',
    sessionData: { s1: { ...emptySessionData, messages } },
  }
  store.set(state)
  const client = { get: vi.fn() } as unknown as ApiClient
  render(<ConversationContainer client={client} store={store} />)
  return store
}

beforeEach(() => {
  counters.rows = {}
  counters.environments = []
})

describe('ConversationContainer re-renders', () => {
  it('re-renders no other tool row when a streamed delta lands', () => {
    const untouched = message('m1', [toolCall('tc-1', 'bash')])
    const streaming = message('m2', [text('looking'), toolCall('tc-2', 'view')])
    const store = setup([untouched, streaming])
    expect(counters.rows).toEqual({ 'tc-1': 1, 'tc-2': 1 })
    // One render per row, and both read the same environment.
    expect(counters.environments).toHaveLength(2)
    expect(new Set(counters.environments).size).toBe(1)

    // A token lands in the last message: the store hands back a new array
    // and a new object for that message, and the untouched message keeps
    // both.
    act(() => store.applyFrame(delta(message('m2', [text('looking harder'), toolCall('tc-2', 'view')]))))
    expect(screen.getByText('looking harder')).toBeInTheDocument()
    // Only the row whose call object changed drew again.
    expect(counters.rows).toEqual({ 'tc-1': 1, 'tc-2': 2 })
    // The environment the rows read kept its identity, so the delta rebuilt
    // neither the tool environment nor the rows that depend on it.
    expect(counters.environments).toHaveLength(3)
    expect(new Set(counters.environments).size).toBe(1)
  })
})

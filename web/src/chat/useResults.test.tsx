import { renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../api/wire'
import { useResults } from './useResults'

function message(parts: WireMessage['parts'], id: string, role = 'assistant'): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

const call: WireMessage = message(
  [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } }],
  'm1',
)

const speaking: WireMessage = message([{ type: 'text', data: { text: 'hi' } }], 'm2')

const streamed: WireMessage = message([{ type: 'text', data: { text: 'hi there' } }], 'm2')

const result = (id: string): WireMessage =>
  message([{ type: 'tool_result', data: { tool_call_id: id, content: 'ok' } }], `t-${id}`, 'tool')

describe('useResults', () => {
  it('answers a result to the call it belongs to', () => {
    const { result: hook } = renderHook(({ messages }) => useResults(messages), {
      initialProps: { messages: [call, speaking, result('tc-1')] },
    })
    expect(hook.current.get('tc-1')?.content).toBe('ok')
  })

  it('keeps the index when only a streamed token changes', () => {
    const { result: hook, rerender } = renderHook(({ messages }) => useResults(messages), {
      initialProps: { messages: [call, speaking, result('tc-1')] },
    })
    const before = hook.current
    rerender({ messages: [call, streamed, result('tc-1')] })
    // A fresh index on every delta would re-render every memoised message.
    expect(hook.current).toBe(before)
  })

  it('rebuilds the index when a result arrives', () => {
    const { result: hook, rerender } = renderHook(({ messages }) => useResults(messages), {
      initialProps: { messages: [call, speaking] },
    })
    const before = hook.current
    rerender({ messages: [call, speaking, result('tc-1')] })
    expect(hook.current).not.toBe(before)
    expect(hook.current.get('tc-1')?.content).toBe('ok')
  })
})

import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../store/app'
import Conversation from './Conversation'

// The tool row stands in for everything a message draws: counting its renders
// is how this spec sees a message that rendered again.
const counters = vi.hoisted(() => ({ toolRows: 0 }))

vi.mock('./ToolCallRow', () => ({
  default: ({ name, done }: { name: string; done: boolean }) => {
    counters.toolRows += 1
    return <li>{done ? `${name} answered` : `${name} running`}</li>
  },
}))

const calling: WireMessage = {
  id: 'm1',
  role: 'assistant',
  session_id: 's1',
  parts: [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash' } }],
  created_at: 1,
  updated_at: 1,
}

const speaking: WireMessage = {
  id: 'm2',
  role: 'assistant',
  session_id: 's1',
  parts: [{ type: 'text', data: { text: 'hi' } }],
  created_at: 2,
  updated_at: 2,
}

function result(toolCallID: string): WireMessage {
  return {
    id: 'm3',
    role: 'assistant',
    session_id: 's1',
    parts: [{ type: 'tool_result', data: { tool_call_id: toolCallID, name: 'bash', content: 'ok' } }],
    created_at: 3,
    updated_at: 3,
  }
}

function conversation(messages: WireMessage[]) {
  return (
    <Conversation
      sessionId="s1"
      title="t"
      messages={messages}
      hasMore={false}
      loadingOlder={false}
      onLoadOlder={() => undefined}
    />
  )
}

beforeEach(() => {
  counters.toolRows = 0
})

describe('Conversation re-renders', () => {
  it('leaves the untouched messages alone when one of them changes', () => {
    const { rerender } = render(conversation([calling, speaking]))
    expect(counters.toolRows).toBe(1)

    // A token lands in the last message: the array is new, the other message
    // object is not.
    rerender(conversation([calling, { ...speaking, parts: [{ type: 'text', data: { text: 'hi there' } }] }]))
    expect(screen.getByText('hi there')).toBeInTheDocument()
    expect(counters.toolRows, 'the unchanged message rendered again').toBe(1)
  })

  it('draws the call again when a result answers it', () => {
    const { rerender } = render(conversation([calling, speaking]))
    expect(counters.toolRows).toBe(1)

    rerender(conversation([calling, speaking, result('tc-1')]))
    // The answer changes what the call draws, so memoising must not stop it.
    expect(counters.toolRows).toBe(2)
    expect(screen.getByText('bash answered')).toBeInTheDocument()
  })
})

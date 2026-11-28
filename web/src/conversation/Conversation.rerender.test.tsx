import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../store/app'
import Conversation from './Conversation'

// The tool row stands in for everything a message draws: counting its renders
// is how this spec sees a message that rendered again.
const counters = vi.hoisted(() => ({ toolRows: 0 }))

vi.mock('../tools/ToolCall', () => ({
  default: ({ call, status }: { call: { name: string }; status: string }) => {
    counters.toolRows += 1
    return <li>{`${call.name} ${status}`}</li>
  },
}))

// The footer pass is counted to see the memo: the footers are rebuilt when
// the message list changes, and never while it keeps its identity.
const footerCalls = vi.hoisted(() => ({ computed: 0 }))

vi.mock('../chat/turns', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../chat/turns')>()
  return {
    ...actual,
    turnFooters: (messages: WireMessage[]) => {
      footerCalls.computed += 1
      return actual.turnFooters(messages)
    },
  }
})

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
  footerCalls.computed = 0
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
    expect(screen.getByText('bash done')).toBeInTheDocument()
  })

  // A streamed delta makes a new array, so the footers are rebuilt; the
  // settled message's footer is the same string, so its view keeps its memo
  // and does not render again.
  it('keeps a settled footer still while the last message streams', () => {
    const prompt: WireMessage = {
      id: 'm0',
      role: 'user',
      session_id: 's1',
      parts: [],
      created_at: 1,
      updated_at: 1,
    }
    const finished: WireMessage = {
      ...calling,
      model: 'm',
      parts: [...calling.parts, { type: 'finish', data: { reason: 'end_turn', time: 4 } }],
    }
    const { rerender } = render(conversation([prompt, finished, speaking]))
    expect(counters.toolRows).toBe(1)
    expect(screen.getByText('m · 3s')).toBeInTheDocument()

    rerender(conversation([prompt, finished, { ...speaking, parts: [{ type: 'text', data: { text: 'hi there' } }] }]))
    expect(screen.getByText('m · 3s')).toBeInTheDocument()
    expect(counters.toolRows, 'the settled message with its footer rendered again').toBe(1)
  })

  it('builds the footers once while the message list keeps its identity', () => {
    const messages = [calling, speaking]
    const { rerender } = render(conversation(messages))
    const computed = footerCalls.computed

    rerender(conversation(messages))
    expect(footerCalls.computed, 'the same list rebuilt its footers').toBe(computed)

    rerender(conversation([...messages]))
    expect(footerCalls.computed).toBe(computed + 1)
  })
})

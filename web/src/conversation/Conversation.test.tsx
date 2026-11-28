import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../store/app'
import Conversation from './Conversation'

const messages: WireMessage[] = [
  {
    id: 'm1',
    role: 'user',
    session_id: 's1',
    parts: [{ type: 'text', data: { text: 'hello' } }],
    created_at: 1,
    updated_at: 1,
  },
  {
    id: 'm2',
    role: 'assistant',
    session_id: 's1',
    parts: [{ type: 'text', data: { text: 'hi there' } }],
    created_at: 2,
    updated_at: 2,
  },
]

const older: WireMessage = {
  id: 'm0',
  role: 'user',
  session_id: 's1',
  parts: [{ type: 'text', data: { text: 'first words' } }],
  created_at: 0,
  updated_at: 0,
}

const newer: WireMessage = {
  id: 'm3',
  role: 'assistant',
  session_id: 's1',
  parts: [{ type: 'text', data: { text: 'one more' } }],
  created_at: 3,
  updated_at: 3,
}

function call(id: string, name: string, finished = false): WireMessage['parts'][number] {
  return { type: 'tool_call', data: { id, name, input: '{}', finished } }
}

function result(toolCallID: string): WireMessage['parts'][number] {
  return { type: 'tool_result', data: { tool_call_id: toolCallID, name: 'bash', content: 'ok' } }
}

let scrollIntoView: ReturnType<typeof vi.fn>

beforeEach(() => {
  // jsdom does not implement scrollIntoView, so every scroll the conversation
  // asks for is the spy.
  scrollIntoView = vi.fn()
  Element.prototype.scrollIntoView = scrollIntoView as unknown as Element['scrollIntoView']
})

afterEach(() => {
  Reflect.deleteProperty(Element.prototype, 'scrollIntoView')
})

// atTop and atBottom replace the layout jsdom does not compute, so the log's
// scroll position can be described to the component.
function layout(log: HTMLElement, scrollTop: number, scrollHeight = 1000, clientHeight = 300) {
  Object.defineProperty(log, 'scrollHeight', { configurable: true, value: scrollHeight })
  Object.defineProperty(log, 'clientHeight', { configurable: true, value: clientHeight })
  Object.defineProperty(log, 'scrollTop', { configurable: true, value: scrollTop })
}

describe('Conversation', () => {
  it('renders the session title and its messages', () => {
    render(
      <Conversation
        sessionId="s1"
        title="Port the parser"
        messages={messages}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(screen.getByRole('heading', { name: 'Port the parser' })).toBeInTheDocument()
    expect(screen.getByText('hello')).toBeInTheDocument()
    expect(screen.getByText('hi there')).toBeInTheDocument()
  })

  it('offers the older page only when there is one', async () => {
    const onLoadOlder = vi.fn()
    render(<Conversation sessionId="s1" title="t" messages={messages} hasMore loadingOlder={false} onLoadOlder={onLoadOlder} />)
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(onLoadOlder).toHaveBeenCalled()
  })

  it('hides the button once the first page is the whole history', () => {
    render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
  })

  it('disables the button while a page is in flight', () => {
    render(<Conversation sessionId="s1" title="t" messages={messages} hasMore loadingOlder onLoadOlder={vi.fn()} />)
    const button = screen.getByRole('button', { name: 'Loading…' })
    expect(button).toBeDisabled()
  })

  it('shows the reason an older page failed', () => {
    render(
      <Conversation
        sessionId="s1"
        title="t"
        messages={messages}
        hasMore
        loadingOlder={false}
        error="unknown_message"
        onLoadOlder={vi.fn()}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('unknown_message')
  })

  it('asks for a session when none is open', () => {
    render(
      <Conversation sessionId={null} title="" messages={[]} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.getByText('Select a session')).toBeInTheDocument()
  })

  it('names a session that has no title yet', () => {
    render(
      <Conversation sessionId="s9" title="" messages={[]} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.getByRole('heading', { name: 'Untitled session' })).toBeInTheDocument()
    expect(screen.queryByText('Select a session')).toBeNull()
    expect(screen.queryAllByRole('article')).toHaveLength(0)
  })

  it('marks a tool call done from a result in a later message', () => {
    render(
      <Conversation
        sessionId="s1"
        title="t"
        messages={[
          { ...messages[1], parts: [call('tc-1', 'bash', true)] },
          { ...messages[0], parts: [result('tc-1')] },
        ]}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.queryByText('Running')).toBeNull()
    // The result-only message draws nothing of its own.
    expect(screen.getAllByRole('article')).toHaveLength(1)
  })

  it('runs only the turn after the last user message of a live session', () => {
    render(
      <Conversation
        sessionId="s1"
        title="t"
        messages={[
          { ...messages[0], parts: [call('tc-1', 'bash', true)] },
          { ...messages[1], parts: [call('tc-2', 'view', true)] },
        ]}
        hasMore={false}
        loadingOlder={false}
        status="running"
        onLoadOlder={vi.fn()}
      />,
    )
    // The earlier turn has moved past its call; only the last one can run.
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByText('Running')).toBeInTheDocument()
  })

  it('does not move the scroll when an older page is prepended', () => {
    const { rerender } = render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    scrollIntoView.mockClear()
    rerender(
      <Conversation
        sessionId="s1"
        title="t"
        messages={[older, ...messages]}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(scrollIntoView).not.toHaveBeenCalled()
  })

  it('follows a new message while the log is at its end', () => {
    const { rerender } = render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    scrollIntoView.mockClear()
    rerender(
      <Conversation
        sessionId="s1"
        title="t"
        messages={[...messages, newer]}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(scrollIntoView).toHaveBeenCalled()
  })

  it('leaves a log the person scrolled up where it is', () => {
    const { rerender } = render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    const log = screen.getByTestId('conversation-log')
    layout(log, 0)
    fireEvent.scroll(log)
    scrollIntoView.mockClear()
    rerender(
      <Conversation
        sessionId="s1"
        title="t"
        messages={[...messages, newer]}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(scrollIntoView).not.toHaveBeenCalled()
  })

  it('opens a new session at its end', () => {
    const { rerender } = render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    scrollIntoView.mockClear()
    rerender(
      <Conversation
        sessionId="s2"
        title="other"
        messages={[newer]}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={vi.fn()}
      />,
    )
    expect(scrollIntoView).toHaveBeenCalled()
  })
})

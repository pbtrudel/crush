import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

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

  it('renders an empty log for a session with no messages yet', () => {
    render(
      <Conversation sessionId="s9" title="" messages={[]} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.getByRole('region', { name: 'Conversation' })).toBeInTheDocument()
    expect(screen.queryByText('Select a session')).toBeNull()
    expect(screen.queryAllByRole('article')).toHaveLength(0)
  })
})

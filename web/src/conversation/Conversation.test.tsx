import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { streamedSourceInterval } from '../chat/useStreamedSource'
import type { WireMessage } from '../store/app'
import { scrollState, stubLayout } from '../test/layout'
import { ToolEnvironmentProvider } from '../tools/environment'
import Conversation, { type ConversationProps } from './Conversation'

function entry(id: string, value: string, role = 'assistant'): WireMessage {
  return {
    id,
    role,
    session_id: 's1',
    parts: [{ type: 'text', data: { text: value } }],
    created_at: 1,
    updated_at: 1,
  }
}

const messages = [entry('m1', 'hello', 'user'), entry('m2', 'hi there')]

function props(overrides: Partial<ConversationProps> = {}): ConversationProps {
  return {
    sessionId: 's1',
    title: 't',
    messages,
    hasMore: false,
    loadingOlder: false,
    onLoadOlder: vi.fn(),
    ...overrides,
  }
}

// articles is the log's message list in the order it renders. The copy
// action belongs to the message's frame, not to the text it draws, so its
// caption is taken out before the text is read.
function articles(): string[] {
  return screen.getAllByRole('article').map((element) => {
    const copy = element.cloneNode(true) as HTMLElement
    for (const button of copy.querySelectorAll('button[aria-label="Copy message"]')) {
      button.remove()
    }
    return copy.textContent ?? ''
  })
}

let restore: () => void = () => undefined

beforeEach(() => {
  restore = stubLayout({ viewport: 600, row: 60 })
})

afterEach(() => {
  restore()
  vi.useRealTimers()
})

describe('Conversation', () => {
  it('renders the session title and its messages in order', () => {
    render(<Conversation {...props({ title: 'Port the parser' })} />)
    expect(screen.getByRole('heading', { name: 'Port the parser' })).toBeInTheDocument()
    expect(articles()).toEqual(['hello', 'hi there'])
  })

  it('asks for a session when none is open', () => {
    render(<Conversation {...props({ sessionId: null })} />)
    expect(screen.getByText('Select a session')).toBeInTheDocument()
  })

  it('names a session that has no title yet', () => {
    render(<Conversation {...props({ title: '' })} />)
    expect(screen.getByRole('heading', { name: 'Untitled session' })).toBeInTheDocument()
  })

  it('offers the older page only when there is one', async () => {
    const onLoadOlder = vi.fn()
    const { rerender } = render(<Conversation {...props({ hasMore: true, onLoadOlder })} />)
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(onLoadOlder).toHaveBeenCalledOnce()
    rerender(<Conversation {...props({ hasMore: false })} />)
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
    expect(screen.getByText('Start of the session')).toBeInTheDocument()
  })

  it('disables the button while a page is in flight and shows a failure', () => {
    render(<Conversation {...props({ hasMore: true, loadingOlder: true, error: 'no such session' })} />)
    expect(screen.getByRole('button', { name: 'Loading…' })).toBeDisabled()
    expect(screen.getByRole('alert')).toHaveTextContent('no such session')
  })

  it('names the log for assistive technology', () => {
    render(<Conversation {...props()} />)
    expect(screen.getByRole('log', { name: 'Conversation log' })).toBeInTheDocument()
  })

  it('renders only the rows in view', () => {
    const many = Array.from({ length: 300 }, (_, index) => entry(`m${index}`, `message ${index}`))
    render(<Conversation {...props({ messages: many })} />)
    const rendered = screen.getAllByRole('article').length
    expect(rendered).toBeGreaterThan(0)
    expect(rendered).toBeLessThan(40)
  })

  it('follows a reply that grows in place while the log is at its end', () => {
    const { rerender } = render(<Conversation {...props()} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 4400, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    ;(log as { scrollHeight: number }).scrollHeight = 5600
    // Same id, more text: M1 followed only a new id.
    rerender(
      <Conversation {...props({ messages: [messages[0], entry('m2', 'hi there, and more')] })} />,
    )
    expect(log.scrollTop).toBe(5600)
    expect(screen.queryByRole('button', { name: 'Back to bottom' })).toBeNull()
  })

  it('stays where the reader scrolled while a reply grows, and jumps back on request', async () => {
    const { rerender } = render(<Conversation {...props()} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 1000, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    ;(log as { scrollHeight: number }).scrollHeight = 5600
    rerender(
      <Conversation {...props({ messages: [messages[0], entry('m2', 'hi there, and more')] })} />,
    )
    expect(log.scrollTop).toBe(1000)
    await userEvent.click(screen.getByRole('button', { name: 'Back to bottom' }))
    expect(log.scrollTop).toBe(5600)
  })

  it('keeps the old first message in place when an older page is prepended', () => {
    vi.useFakeTimers()
    const { rerender } = render(<Conversation {...props({ hasMore: true })} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 1000, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    // The virtualizer measures no new row while a scroll is in flight, and
    // holds that state for its reset delay. A page arrives after the
    // reader's scroll has settled, not inside it.
    act(() => {
      vi.advanceTimersByTime(200)
    })
    rerender(
      <Conversation
        {...props({ messages: [entry('m0', 'first words', 'user'), ...messages], hasMore: false })}
      />,
    )
    // Every row measures 60: the old first message moved down one row.
    expect(log.scrollTop).toBe(1060)
  })

  it('marks a tool call done from a result in a later message', () => {
    render(
      <Conversation
        {...props({
          messages: [
            {
              ...messages[1],
              parts: [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } }],
            },
            { ...messages[0], id: 'm3', parts: [{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }] },
          ],
        })}
      />,
    )
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.getAllByRole('article')).toHaveLength(1)
  })

  it('runs only the turn after the last user message of a live session', () => {
    render(
      <Conversation
        {...props({
          status: 'running',
          messages: [
            { ...messages[0], parts: [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } }] },
            { ...messages[1], parts: [{ type: 'tool_call', data: { id: 'tc-2', name: 'view', input: '{}', finished: true } }] },
          ],
        })}
      />,
    )
    // The earlier turn has moved past its call; only the last one can run.
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByText('Running')).toBeInTheDocument()
  })

  it('keeps keyboard focus inside an open tool row while a reply streams', async () => {
    const longError = Array.from({ length: 50 }, (_, index) => `error ${index + 1}`).join('\n')
    const calling: WireMessage = {
      ...messages[1],
      id: 'm1',
      parts: [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{"command":"ls"}', finished: true } }],
    }
    const failed: WireMessage = {
      ...messages[0],
      id: 'm2',
      role: 'tool',
      parts: [
        { type: 'tool_result', data: { tool_call_id: 'tc-1', name: 'bash', content: longError, is_error: true } },
      ],
    }
    const { rerender } = render(
      <ToolEnvironmentProvider>
        <Conversation {...props({ messages: [calling, failed, entry('m3', 'first words')] })} />
      </ToolEnvironmentProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Bash/ }))
    const showAll = screen.getByRole('button', { name: 'Show all 50 lines' })
    showAll.focus()
    expect(showAll).toHaveFocus()
    rerender(
      <ToolEnvironmentProvider>
        <Conversation
          {...props({ messages: [calling, failed, entry('m3', 'first words, and more')] })}
        />
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByRole('button', { name: 'Show all 50 lines' })).toBe(showAll)
    expect(showAll).toHaveFocus()
  })

  it('streams under StrictMode without losing the newest text', () => {
    vi.useFakeTimers()
    const { rerender } = render(
      <StrictMode>
        <Conversation {...props({ status: 'running' })} />
      </StrictMode>,
    )
    rerender(
      <StrictMode>
        <Conversation
          {...props({ status: 'running', messages: [messages[0], entry('m2', 'hi there, and more')] })}
        />
      </StrictMode>,
    )
    // The streaming row holds the new text back for one pass, then shows it.
    expect(screen.queryByText('hi there, and more')).toBeNull()
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    expect(screen.getByText('hi there, and more')).toBeInTheDocument()
  })

  // The header carries the context ring, the tokens in use and the cost.
  it('shows the session usage in its header', () => {
    render(
      <Conversation
        {...props({
          usage: { promptTokens: 43800, completionTokens: 0, cost: 0.5, contextWindow: 200000 },
        })}
      />,
    )
    const summary = screen.getByTestId('usage-summary')
    expect(summary).toHaveTextContent('43.8K tokens')
    expect(summary).toHaveTextContent('$0.50')
  })

  it('reads the footer under the message that ends a turn', () => {
    const turn: WireMessage[] = [
      { id: 'u1', role: 'user', session_id: 's1', parts: [], created_at: 100, updated_at: 100 },
      {
        id: 'a1',
        role: 'assistant',
        session_id: 's1',
        parts: [
          { type: 'text', data: { text: 'Done.' } },
          { type: 'finish', data: { reason: 'end_turn', time: 125 } },
        ],
        created_at: 101,
        updated_at: 125,
        model: 'claude-sonnet',
      },
    ]
    render(<Conversation {...props({ messages: turn })} />)
    expect(screen.getByText('claude-sonnet · 25s')).toBeInTheDocument()
    expect(screen.getByText('claude-sonnet · 25s').closest('article')).toHaveAttribute(
      'data-role',
      'assistant',
    )
  })
})

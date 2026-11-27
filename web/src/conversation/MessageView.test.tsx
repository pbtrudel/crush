import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../store/app'
import MessageView from './MessageView'

function message(parts: WireMessage['parts'], role = 'assistant'): WireMessage {
  return { id: 'm1', role, session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

function text(value: string) {
  return { type: 'text', data: { text: value } }
}

function call(id: string, name: string, finished = false) {
  return { type: 'tool_call', data: { id, name, input: '{}', finished } }
}

describe('MessageView', () => {
  it('renders the text of a user message', () => {
    render(<MessageView message={message([text('Refactor the parser')], 'user')} />)
    expect(screen.getByText('Refactor the parser')).toBeInTheDocument()
  })

  it('collapses reasoning behind a summary', () => {
    render(
      <MessageView
        message={message([
          { type: 'reasoning', data: { thinking: 'Look at parser.go first.' } },
          text('Reading it.'),
        ])}
      />,
    )
    const details = screen.getByText('Reasoning').closest('details')
    expect(details).not.toBeNull()
    expect(details?.open).toBe(false)
    expect(screen.getByText('Look at parser.go first.')).toBeInTheDocument()
    expect(screen.getByText('Reading it.')).toBeInTheDocument()
  })

  it('hides a model-only text part', () => {
    // The environment snapshot, the reminders and the compaction state are
    // hidden text: the model history keeps them, the screen does not.
    const { container } = render(
      <MessageView message={message([{ type: 'text', data: { text: 'environment snapshot', hidden: true } }])} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('keeps the visible parts of a message that also has hidden ones', () => {
    render(
      <MessageView
        message={message([{ type: 'text', data: { text: '<reminders>', hidden: true } }, text('Your turn.')], 'user')}
      />,
    )
    expect(screen.getByText('Your turn.')).toBeInTheDocument()
    expect(screen.queryByText('<reminders>')).toBeNull()
  })

  it('renders nothing for a message whose parts are all tool results', () => {
    const { container } = render(
      <MessageView
        message={message(
          [{ type: 'tool_result', data: { tool_call_id: 'tc-1', name: 'bash', content: 'ok' } }],
          'user',
        )}
      />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('renders one row per tool call', () => {
    render(<MessageView message={message([call('tc-1', 'bash'), call('tc-2', 'view')])} toolResults={new Set(['tc-1'])} />)
    expect(screen.getByText('bash')).toBeInTheDocument()
    expect(screen.getByText('view')).toBeInTheDocument()
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  it('shows done only for a call that answered', () => {
    // `finished` says the input stopped streaming, not that the tool ran, so
    // both calls carry it and only the one with a result is done.
    render(
      <MessageView
        message={message([call('tc-1', 'bash', true), call('tc-2', 'view', true)])}
        toolResults={new Set(['tc-1'])}
      />,
    )
    expect(screen.getAllByText('done')).toHaveLength(1)
    expect(screen.getByText('running')).toBeInTheDocument()
    const done = screen.getByText('done').closest('li')
    expect(done).toHaveTextContent('bash')
  })

  it('shows running for a call with no result at all', () => {
    render(<MessageView message={message([call('tc-3', 'grep', true)])} />)
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.queryByText('done')).toBeNull()
  })

  it('renders nothing it does not understand', () => {
    const { container } = render(<MessageView message={message([{ type: 'unknown_part', data: {} }])} />)
    expect(container.querySelectorAll('[data-testid="tool-call"]')).toHaveLength(0)
  })

  it('renders markup in the text as text', () => {
    const { container } = render(
      <MessageView message={message([text('<img src="x" onerror="alert(1)"> hello')])} />,
    )
    expect(screen.getByText('<img src="x" onerror="alert(1)"> hello')).toBeInTheDocument()
    expect(container.querySelector('img')).toBeNull()
  })
})

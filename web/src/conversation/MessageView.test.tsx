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

  it('renders one row per tool call with its status', () => {
    render(
      <MessageView
        message={message([
          { type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } },
          { type: 'tool_call', data: { id: 'tc-2', name: 'view', input: '{}' } },
        ])}
      />,
    )
    expect(screen.getByText('bash')).toBeInTheDocument()
    expect(screen.getByText('done')).toBeInTheDocument()
    expect(screen.getByText('view')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
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

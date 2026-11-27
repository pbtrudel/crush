import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import Sidebar, { type SidebarProps } from './Sidebar'

const sessions = [
  { id: 's2', parent_session_id: '', title: 'Write parser tests', message_count: 0, updated_at: 20, status: 'queued' as const },
  { id: 's1', parent_session_id: '', title: 'Port the parser', message_count: 2, updated_at: 10, status: 'idle' as const },
]

function props(overrides: Partial<SidebarProps> = {}): SidebarProps {
  return {
    workspaceName: 'crush',
    branch: 'main',
    sessions,
    selectedSessionId: 's1',
    unread: () => false,
    error: null,
    onSelect: vi.fn(),
    onCreate: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onCopyId: vi.fn(),
    onOpenInTerminal: vi.fn(),
    ...overrides,
  }
}

describe('Sidebar', () => {
  it('shows the workspace name and branch', () => {
    render(<Sidebar {...props()} />)
    expect(screen.getByText('crush')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  it('lists the sessions in the order it was given', () => {
    render(<Sidebar {...props()} />)
    const titles = screen.getAllByTestId('session-title').map((node) => node.textContent)
    expect(titles).toEqual(['Write parser tests', 'Port the parser'])
  })

  it('creates a session', async () => {
    const onCreate = vi.fn()
    render(<Sidebar {...props({ onCreate })} />)
    await userEvent.click(screen.getByRole('button', { name: 'New Session' }))
    expect(onCreate).toHaveBeenCalled()
  })

  it('shows the refusal the gateway sent', () => {
    render(<Sidebar {...props({ error: 'The terminal is showing this session; switch it first' })} />)
    expect(screen.getByRole('alert')).toHaveTextContent('switch it first')
  })
})

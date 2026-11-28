import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import SessionRow, { type SessionRowProps } from './SessionRow'

const row = {
  id: 's-m1-run',
  parent_session_id: '',
  title: 'Port the parser',
  message_count: 2,
  updated_at: 20,
  status: 'running' as const,
}

function props(overrides: Partial<SessionRowProps> = {}): SessionRowProps {
  return {
    row,
    selected: false,
    unread: false,
    onSelect: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onCopyId: vi.fn(),
    onOpenInTerminal: vi.fn(),
    ...overrides,
  }
}

describe('SessionRow', () => {
  it('shows the title and the running marker', () => {
    render(<SessionRow {...props()} />)
    expect(screen.getByText('Port the parser')).toBeInTheDocument()
    expect(screen.getByLabelText('Running')).toBeInTheDocument()
  })

  it('shows the queued, waiting and unread markers', () => {
    render(<SessionRow {...props({ row: { ...row, status: 'queued' }, unread: true })} />)
    expect(screen.getByLabelText('Queued')).toBeInTheDocument()
    expect(screen.getByLabelText('Unread')).toBeInTheDocument()

    render(<SessionRow {...props({ row: { ...row, status: 'pending_interaction' } })} />)
    expect(screen.getByLabelText('Waiting for you')).toBeInTheDocument()
  })

  it('selects on click', async () => {
    const onSelect = vi.fn()
    render(<SessionRow {...props({ onSelect })} />)
    await userEvent.click(screen.getByText('Port the parser'))
    expect(onSelect).toHaveBeenCalledWith('s-m1-run')
  })

  it('renames inline on Enter', async () => {
    const onRename = vi.fn()
    render(<SessionRow {...props({ onRename })} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    await userEvent.clear(input)
    await userEvent.type(input, 'Renamed{Enter}')
    expect(onRename).toHaveBeenCalledWith('s-m1-run', 'Renamed')
  })

  it('renames once when Enter commits the draft', async () => {
    const onRename = vi.fn()
    render(<SessionRow {...props({ onRename })} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    await userEvent.clear(input)
    await userEvent.type(input, 'Renamed{Enter}')
    expect(onRename).toHaveBeenCalledTimes(1)
  })

  it('leaves the title alone on Escape', async () => {
    const onRename = vi.fn()
    render(<SessionRow {...props({ onRename })} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    await userEvent.clear(input)
    await userEvent.type(input, 'Discarded{Escape}')
    expect(onRename).not.toHaveBeenCalled()
    expect(screen.getByText('Port the parser')).toBeInTheDocument()
  })

  it('marks an Escape it handled as taken', async () => {
    render(<SessionRow {...props()} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    // A listener above the row (the narrow drawer listens on the window) must
    // be able to tell that the key was spent on the draft. fireEvent reports
    // whether the event survived un-cancelled.
    expect(fireEvent.keyDown(input, { key: 'Escape' })).toBe(false)
    expect(screen.queryByLabelText('Session title')).toBeNull()
  })

  it('opens the draft at the title the row has now', async () => {
    const onRename = vi.fn()
    const { rerender } = render(<SessionRow {...props({ onRename })} />)
    // A rename from another tab or the terminal lands on the prop while the
    // row is mounted; the draft must not resurrect the title it mounted with.
    rerender(<SessionRow {...props({ onRename, row: { ...row, title: 'Renamed in another tab' } })} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    expect(input).toHaveValue('Renamed in another tab')
    fireEvent.blur(input)
    expect(onRename).not.toHaveBeenCalled()
  })

  it('caps the rename draft at 200 characters', async () => {
    render(<SessionRow {...props()} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    expect(screen.getByLabelText('Session title')).toHaveAttribute('maxlength', '200')
  })

  it('disables delete while its request is in flight', async () => {
    let finish: () => void = () => undefined
    const onDelete = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    )
    render(<SessionRow {...props({ onDelete })} />)
    const button = screen.getByLabelText('Delete session')
    await userEvent.click(button)
    expect(onDelete).toHaveBeenCalledWith('s-m1-run')
    expect(button).toBeDisabled()
    finish()
    await waitFor(() => expect(button).toBeEnabled())
  })

  it('offers delete, copy and open in terminal', async () => {
    const onDelete = vi.fn()
    const onCopyId = vi.fn()
    const onOpenInTerminal = vi.fn()
    render(<SessionRow {...props({ onDelete, onCopyId, onOpenInTerminal })} />)
    await userEvent.click(screen.getByLabelText('Delete session'))
    await userEvent.click(screen.getByLabelText('Copy session ID'))
    await userEvent.click(screen.getByLabelText('Open in terminal'))
    expect(onDelete).toHaveBeenCalledWith('s-m1-run')
    expect(onCopyId).toHaveBeenCalledWith('s-m1-run')
    expect(onOpenInTerminal).toHaveBeenCalledWith('s-m1-run')
  })
})

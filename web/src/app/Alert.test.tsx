import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { createAppStore, type AppState, type AppStore } from '../store/app'
import type { Store } from '../store/store'
import { memoryStorage } from './storage'
import Alert from './Alert'

function storeWith(sessionId: string, message: string, selected: string | null): Store<AppState> & AppStore {
  const store = createAppStore(memoryStorage())
  store.set({ selectedSessionId: selected })
  store.applyFrame({ seq: 1, type: 'web_error', payload: { session_id: sessionId, code: 'agent_busy', error: message } })
  return store
}

describe('Alert', () => {
  it('shows a global error whichever session is selected', () => {
    render(<Alert store={storeWith('', 'the gateway is busy', 's1')} />)
    expect(screen.getByRole('alert')).toHaveTextContent('the gateway is busy')
  })

  it('shows an error that names a session only while it is selected', () => {
    const store = storeWith('s2', 'that session is busy', 's1')
    render(<Alert store={store} />)
    expect(screen.queryByRole('alert')).toBeNull()
    act(() => store.selectSession('s2'))
    expect(screen.getByRole('alert')).toHaveTextContent('that session is busy')
  })

  it('keeps the Dismiss button out of the live region', () => {
    render(<Alert store={storeWith('', 'the gateway is busy', null)} />)
    const alert = screen.getByRole('alert')
    expect(within(alert).getByText('the gateway is busy')).toBeInTheDocument()
    expect(within(alert).queryByRole('button')).toBeNull()
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
  })

  it('dismisses the error and moves focus to the content region', async () => {
    const user = userEvent.setup()
    const store = storeWith('', 'the gateway is busy', null)
    render(
      <main tabIndex={-1} data-testid="center">
        <Alert store={store} />
      </main>,
    )
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(store.get().error).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(document.activeElement).toBe(screen.getByTestId('center'))
  })

  it('renders nothing before an error arrives', () => {
    render(<Alert store={createAppStore(memoryStorage())} />)
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

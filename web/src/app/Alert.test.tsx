import { act, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { createAppStore, type AppState, type AppStore } from '../store/app'
import type { Store } from '../store/store'
import { memoryStorage } from '../test/memoryStorage'
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

  it('renders nothing before an error arrives', () => {
    render(<Alert store={createAppStore(memoryStorage())} />)
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

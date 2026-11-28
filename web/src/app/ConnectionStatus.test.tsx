import { act, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { ConnectionState } from '../api/stream'
import { createAppStore } from '../store/app'
import { memoryStorage } from './storage'
import ConnectionStatus from './ConnectionStatus'

function storeWith(connection: ConnectionState) {
  const store = createAppStore(memoryStorage())
  store.setConnection(connection)
  return store
}

describe('ConnectionStatus', () => {
  it('says nothing while the stream is open', () => {
    render(<ConnectionStatus store={storeWith('open')} />)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('says it is reconnecting', () => {
    render(<ConnectionStatus store={storeWith('reconnecting')} />)
    expect(screen.getByRole('status')).toHaveTextContent('Reconnecting…')
  })

  it('says Crush has stopped once the gateway announces it', () => {
    const store = storeWith('open')
    render(<ConnectionStatus store={store} />)
    expect(screen.queryByRole('status')).toBeNull()
    act(() => store.applyFrame({ seq: 1, type: 'gateway_closing', payload: { reason: 'the terminal quit' } }))
    expect(screen.getByRole('status')).toHaveTextContent('Crush has stopped. Run /web in Crush to reconnect.')
  })

  it('says Crush has stopped when the stream closed', () => {
    render(<ConnectionStatus store={storeWith('closed')} />)
    expect(screen.getByRole('status')).toHaveTextContent('Crush has stopped. Run /web in Crush to reconnect.')
  })
})

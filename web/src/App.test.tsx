import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import App from './App'
import type { ApiClient } from './api/http'
import type { StreamClient } from './api/stream'
import { useGateway, type Gateway } from './app/useGateway'
import { createAppStore } from './store/app'
import { memoryStorage } from './test/memoryStorage'

vi.mock('./app/useGateway', () => ({ useGateway: vi.fn() }))

function gateway(overrides: Partial<Gateway> = {}): Gateway {
  return {
    state: 'ready',
    error: null,
    client: { send: vi.fn() } as unknown as ApiClient,
    stream: {} as StreamClient,
    store: createAppStore(memoryStorage()),
    ...overrides,
  }
}

describe('App', () => {
  beforeEach(() => {
    vi.mocked(useGateway).mockReturnValue(gateway())
  })

  it('shows the preview banner once the tab is ready', () => {
    render(<App />)
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
  })

  it('offers the session sidebar once the tab is ready', () => {
    render(<App />)
    expect(screen.getByRole('navigation', { name: 'Sessions' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Session' })).toBeInTheDocument()
  })

  it('shows the connecting line while the tab authenticates', () => {
    vi.mocked(useGateway).mockReturnValue(gateway({ state: 'authenticating' }))
    render(<App />)
    expect(screen.getByText(/connecting to crush/i)).toBeInTheDocument()
  })

  it('shows the unauthorized screen without a token', () => {
    vi.mocked(useGateway).mockReturnValue(gateway({ state: 'unauthorized', error: 'This tab has no token.' }))
    render(<App />)
    expect(screen.getByText(/no valid token/i)).toBeInTheDocument()
    expect(screen.getByText('/web')).toBeInTheDocument()
  })

  it('shows the unreachable screen with the reason the gateway gave', () => {
    vi.mocked(useGateway).mockReturnValue(gateway({ state: 'unreachable', error: 'fetch failed' }))
    render(<App />)
    expect(screen.getByText('Crush is not answering')).toBeInTheDocument()
    expect(screen.getByText('fetch failed')).toBeInTheDocument()
    // The launch token is gone from the address bar by now, so the only way
    // back is a fresh link from the terminal.
    expect(screen.getByText('/web')).toBeInTheDocument()
  })
})

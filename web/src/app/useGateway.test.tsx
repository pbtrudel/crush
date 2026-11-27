import { StrictMode, type ReactNode } from 'react'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import App from '../App'
import { HttpError } from '../api/http'
import { StreamClient } from '../api/stream'
import type { SessionRow, SessionStatus } from '../api/wire'
import { maxSubscriptions } from '../store/app'
import { memoryStorage } from '../test/memoryStorage'
import { useGateway } from './useGateway'

class FakeSocket {
  readyState = 1
  closed = false
  sent: string[] = []
  onopen: (() => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null

  send(data?: unknown): void {
    if (typeof data === 'string') {
      this.sent.push(data)
    }
  }

  close(): void {
    this.closed = true
    this.readyState = 3
  }

  // fail is the browser's view of a refused handshake: the gateway's 401 is
  // hidden and the socket closes with code 1006 before it ever opens.
  fail(code = 1006): void {
    this.readyState = 3
    this.onclose?.({ code })
  }

  open(): void {
    this.onopen?.()
  }
}

// harness builds the options useGateway takes. Each call records the socket
// the stream opened, so a test can read what the tab sent and whether it
// closed the connection.
function harness(
  hash: string,
  authenticated: boolean,
  health: () => Promise<unknown> = async () => ({ ok: true }),
) {
  const client = {
    authenticated,
    authenticate: vi.fn(async () => undefined),
    get: vi.fn(async (_path: string) => health()),
    wsProtocols: () => ['crush.v2', 'crush.auth.token'],
    clearToken: vi.fn(),
  }
  const sockets: FakeSocket[] = []
  return {
    client,
    sockets,
    get socket() {
      return sockets[0]
    },
    options: {
      client,
      hash,
      stripHash: vi.fn(),
      storage: memoryStorage(),
      createSocket: () => {
        const socket = new FakeSocket()
        sockets.push(socket)
        return socket as unknown as WebSocket
      },
    },
  }
}

function sessionRow(id: string, status: SessionStatus = 'idle'): SessionRow {
  return { id, parent_session_id: '', title: id, message_count: 0, updated_at: 1, status }
}

describe('useGateway', () => {
  it('becomes ready and starts the stream', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    expect(result.current.stream.state).toBe('connecting')
    expect(h.sockets).toHaveLength(1)
    unmount()
  })

  it('stops on the unauthorized screen without a token', async () => {
    const h = harness('', false)
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.sockets).toHaveLength(0)
  })

  it('reports an unreachable gateway as a network error, not a token one', async () => {
    const h = harness('#t=abc123', false, async () => Promise.reject(new Error('offline')))
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unreachable'))
    expect(result.current.error).toBe('offline')
    expect(h.sockets).toHaveLength(0)
    unmount()
  })

  it('shows the unauthorized screen when the handshake is refused', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.client.get.mockRejectedValueOnce(new HttpError(401, 'unauthorized', 'missing or invalid token'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.client.clearToken).toHaveBeenCalled()
    // The stream is stopped too: no backoff loop keeps offering a token the
    // gateway has already refused.
    expect(result.current.stream.state).toBe('closed')
    unmount()
  })

  it('keeps reconnecting when the probe finds a live gateway', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.client.get.mockResolvedValueOnce({ ok: true })
    act(() => h.socket.fail(1006))
    // The second health call is the probe the close triggered.
    await waitFor(() => expect(h.client.get).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')
    expect(h.client.clearToken).not.toHaveBeenCalled()
    unmount()
  })

  it('probes again on a later refused handshake after an outage', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    // The gateway is down: the first refused close probes and learns nothing.
    h.client.get.mockRejectedValueOnce(new Error('offline'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(h.client.get).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')

    // A new gateway answers on the same port with a new token: the next
    // attempt is refused again, and this probe sees the 401.
    h.client.get.mockRejectedValueOnce(new HttpError(401, 'unauthorized', 'missing or invalid token'))
    await waitFor(() => expect(h.sockets).toHaveLength(2))
    act(() => h.sockets[1].fail(1006))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.client.get).toHaveBeenCalledTimes(3)
    expect(h.client.clearToken).toHaveBeenCalled()
    expect(result.current.stream.state).toBe('closed')
    unmount()
  })

  it('keeps reconnecting when the probe cannot reach the gateway', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.client.get.mockRejectedValueOnce(new Error('offline'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(h.client.get).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')
    expect(h.client.clearToken).not.toHaveBeenCalled()
    unmount()
  })

  it('strips the launch token from the address bar once it is exchanged', async () => {
    window.history.replaceState(null, '', '/#t=abc123')
    const h = harness('', false)
    const { result, unmount } = renderHook(() =>
      useGateway({ client: h.client, storage: memoryStorage(), createSocket: h.options.createSocket }),
    )
    try {
      await waitFor(() => expect(result.current.state).toBe('ready'))
      expect(h.client.authenticate).toHaveBeenCalledWith('abc123')
      expect(window.location.hash).toBe('')
    } finally {
      window.history.replaceState(null, '', '/')
      unmount()
    }
  })

  it('drops a stored token the gateway refuses', async () => {
    const h = harness('', true, async () => Promise.reject(new HttpError(401, 'unauthorized', 'missing or invalid token')))
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.client.clearToken).toHaveBeenCalled()
    expect(result.current.client).toBe(h.client)
  })

  it('subscribes the selected session and every session that is not idle', async () => {
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    act(() => {
      result.current.store.set({ sessions: [sessionRow('s1'), sessionRow('s2', 'running')] })
      result.current.store.selectSession('s1')
    })
    const sent = h.socket.sent.map((raw) => JSON.parse(raw) as { type: string; session_id: string })
    expect(sent).toContainEqual({ type: 'subscribe', session_id: 's1' })
    expect(sent).toContainEqual({ type: 'subscribe', session_id: 's2' })
    unmount()
  })

  it('unsubscribes a session it no longer wants and sends nothing for an unrelated change', async () => {
    const subscribe = vi.spyOn(StreamClient.prototype, 'subscribe')
    const unsubscribe = vi.spyOn(StreamClient.prototype, 'unsubscribe')
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    act(() => {
      result.current.store.set({ sessions: [sessionRow('s1'), sessionRow('s2')] })
      result.current.store.selectSession('s1')
    })
    expect(subscribe.mock.calls).toEqual([['s1']])
    // The wanted set did not change, so the diff asks the stream for
    // nothing, even though the store told the hook about the change.
    act(() => result.current.store.set({ connection: 'open' }))
    expect(subscribe.mock.calls).toHaveLength(1)
    expect(unsubscribe.mock.calls).toHaveLength(0)
    act(() => result.current.store.selectSession('s2'))
    expect(unsubscribe.mock.calls).toEqual([['s1']])
    const sent = h.socket.sent.map((raw) => JSON.parse(raw) as { type: string; session_id: string })
    expect(sent).toContainEqual({ type: 'unsubscribe', session_id: 's1' })
    expect(sent).toContainEqual({ type: 'subscribe', session_id: 's2' })
    unmount()
    subscribe.mockRestore()
    unsubscribe.mockRestore()
  })

  it('unsubscribes before it subscribes when the wanted set is full', async () => {
    const subscribe = vi.spyOn(StreamClient.prototype, 'subscribe')
    const unsubscribe = vi.spyOn(StreamClient.prototype, 'unsubscribe')
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    const running = Array.from({ length: maxSubscriptions }, (_, index) => sessionRow(`s${index}`, 'running'))
    act(() => result.current.store.set({ sessions: running }))
    expect(subscribe.mock.calls).toHaveLength(maxSubscriptions)
    subscribe.mockClear()
    unsubscribe.mockClear()

    // The tab sits at the gateway's cap, so the new selection fits only once
    // the running session it displaces has been dropped.
    act(() => result.current.store.selectSession('s32'))
    expect(unsubscribe).toHaveBeenCalledWith('s31')
    expect(subscribe).toHaveBeenCalledWith('s32')
    expect(unsubscribe.mock.invocationCallOrder[0]).toBeLessThan(subscribe.mock.invocationCallOrder[0])
    unmount()
    subscribe.mockRestore()
    unsubscribe.mockRestore()
  })

  it('closes the socket and removes its network listeners on unmount', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    const h = harness('#t=abc123', false)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    const socket = h.socket
    act(() => result.current.store.selectSession('s1'))
    const unsubscribe = vi.spyOn(StreamClient.prototype, 'unsubscribe')
    unmount()
    expect(socket.closed).toBe(true)
    expect(unsubscribe).toHaveBeenCalledWith('s1')
    unsubscribe.mockRestore()
    for (const [type, listener] of add.mock.calls) {
      if (type === 'online' || type === 'offline') {
        expect(remove.mock.calls).toContainEqual([type, listener])
      }
    }
    add.mockRestore()
    remove.mockRestore()
  })

  it('leaves one live socket under StrictMode', async () => {
    const h = harness('#t=abc123', false)
    const wrapper = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>
    const { result, unmount } = renderHook(() => useGateway(h.options), { wrapper })
    await waitFor(() => expect(result.current.state).toBe('ready'))
    // What this pins is the socket count: StrictMode mounts, unmounts and
    // mounts again, and the double effect must not open a second socket.
    // Listener and subscription cleanup is pinned by the unmount test above.
    expect(h.sockets).toHaveLength(1)
    expect(h.sockets.filter((socket) => !socket.closed)).toHaveLength(1)
    unmount()
  })

  it('boots when the browser blocks site data', async () => {
    const local = Object.getOwnPropertyDescriptor(window, 'localStorage')
    const session = Object.getOwnPropertyDescriptor(window, 'sessionStorage')
    const deny = {
      configurable: true,
      get(): Storage {
        throw new DOMException('blocked', 'SecurityError')
      },
    }
    Object.defineProperty(window, 'localStorage', deny)
    Object.defineProperty(window, 'sessionStorage', deny)
    try {
      // The page must not go blank: with no token the tab shows the
      // unauthorized screen instead of throwing before React renders.
      render(<App />)
      expect(await screen.findByText(/no valid token/i)).toBeInTheDocument()
    } finally {
      if (local) {
        Object.defineProperty(window, 'localStorage', local)
      }
      if (session) {
        Object.defineProperty(window, 'sessionStorage', session)
      }
    }
  })
})

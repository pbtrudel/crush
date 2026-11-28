import { StrictMode, type ReactNode } from 'react'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import App from '../App'
import { ApiClient, HttpError } from '../api/http'
import { StreamClient } from '../api/stream'
import { bootstrapSchema, type SessionRow, type SessionStatus } from '../api/wire'
import { maxSubscriptions } from '../store/app'
import { memoryStorage } from './storage'
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

  // receive plays one frame as the gateway would send it.
  receive(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
}

// A bootstrap the committed wire schema accepts: the stream validates every
// frame, so the fixture carries the fields the gateway sends.
const bootstrap = {
  version: 'x',
  working_dir: '/w',
  current_session_id: '',
  sessions: [],
  model: { id: 'm', name: 'n', provider: 'p', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'standard',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
}

// Health answers one /api/health request. A rejected HttpError becomes the
// response the gateway would send; any other rejection is a network failure
// that never reached a gateway.
type HealthAnswer = () => Promise<unknown>

// harness builds the options useGateway takes around a real ApiClient whose
// fetch is stubbed: the hook runs the client the tab boots with, and only the
// network is replaced. Each call records the socket the stream opened, so a
// test can read what the tab sent and whether it closed the connection.
function harness(hash: string, storedToken: string | null, answer: HealthAnswer = async () => ({ ok: true })) {
  const storage = memoryStorage(storedToken === null ? {} : { 'crush.token': storedToken })
  const health = vi.fn(answer)
  const fetchImpl = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const path = String(input)
    if (path === '/api/auth') {
      return new Response(null, { status: 204 })
    }
    if (path === '/api/health') {
      try {
        return new Response(JSON.stringify(await health()), { status: 200 })
      } catch (cause) {
        if (!(cause instanceof HttpError)) {
          throw cause
        }
        return new Response(JSON.stringify({ code: cause.code, error: cause.message }), { status: cause.status })
      }
    }
    return new Response('{}', { status: 404 })
  })
  const client = new ApiClient({ storage, fetchImpl })
  const authenticate = vi.spyOn(client, 'authenticate')
  const clearToken = vi.spyOn(client, 'clearToken')
  const sockets: FakeSocket[] = []
  return {
    client,
    storage,
    health,
    authenticate,
    clearToken,
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
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    expect(result.current.stream.state).toBe('connecting')
    expect(h.sockets).toHaveLength(1)
    unmount()
  })

  it('stops on the unauthorized screen without a token', async () => {
    const h = harness('', null)
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.sockets).toHaveLength(0)
  })

  it('reports an unreachable gateway as a network error, not a token one', async () => {
    const h = harness('#t=abc123', null, async () => Promise.reject(new Error('offline')))
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unreachable'))
    expect(result.current.error).toBe('offline')
    expect(h.sockets).toHaveLength(0)
    unmount()
  })

  it('shows the unauthorized screen when the handshake is refused', async () => {
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.health.mockRejectedValueOnce(new HttpError(401, 'unauthorized', 'missing or invalid token'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.clearToken).toHaveBeenCalled()
    // The stream is stopped too: no backoff loop keeps offering a token the
    // gateway has already refused.
    expect(result.current.stream.state).toBe('closed')
    unmount()
  })

  it('keeps reconnecting when the probe finds a live gateway', async () => {
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.health.mockResolvedValueOnce({ ok: true })
    act(() => h.socket.fail(1006))
    // The second health call is the probe the close triggered.
    await waitFor(() => expect(h.health).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')
    expect(h.clearToken).not.toHaveBeenCalled()
    unmount()
  })

  it('probes again on a later refused handshake after an outage', async () => {
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    // The gateway is down: the first refused close probes and learns nothing.
    h.health.mockRejectedValueOnce(new Error('offline'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(h.health).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')

    // A new gateway answers on the same port with a new token: the next
    // attempt is refused again, and this probe sees the 401.
    h.health.mockRejectedValueOnce(new HttpError(401, 'unauthorized', 'missing or invalid token'))
    await waitFor(() => expect(h.sockets).toHaveLength(2))
    act(() => h.sockets[1].fail(1006))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.health).toHaveBeenCalledTimes(3)
    expect(h.clearToken).toHaveBeenCalled()
    expect(result.current.stream.state).toBe('closed')
    unmount()
  })

  it('keeps reconnecting when the probe cannot reach the gateway', async () => {
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    h.health.mockRejectedValueOnce(new Error('offline'))
    act(() => h.socket.fail(1006))
    await waitFor(() => expect(h.health).toHaveBeenCalledTimes(2))
    expect(result.current.state).toBe('ready')
    expect(result.current.stream.state).toBe('reconnecting')
    expect(h.clearToken).not.toHaveBeenCalled()
    unmount()
  })

  it('strips the launch token from the address bar once it is exchanged', async () => {
    window.history.replaceState(null, '', '/#t=abc123')
    const h = harness('', null)
    const { result, unmount } = renderHook(() =>
      useGateway({ client: h.client, storage: memoryStorage(), createSocket: h.options.createSocket }),
    )
    try {
      await waitFor(() => expect(result.current.state).toBe('ready'))
      expect(h.authenticate).toHaveBeenCalledWith('abc123')
      expect(window.location.hash).toBe('')
    } finally {
      window.history.replaceState(null, '', '/')
      unmount()
    }
  })

  it('drops a stored token the gateway refuses', async () => {
    const h = harness('', 'stored', async () => Promise.reject(new HttpError(401, 'unauthorized', 'missing or invalid token')))
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
    expect(h.clearToken).toHaveBeenCalled()
    expect(result.current.client).toBe(h.client)
  })

  it('subscribes the selected session and every session that is not idle', async () => {
    const h = harness('#t=abc123', null)
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

  it('applies the bootstrap the stream already parsed', async () => {
    const parse = vi.spyOn(bootstrapSchema, 'parse')
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    act(() => h.socket.receive({ seq: 1, type: 'bootstrap', payload: bootstrap }))
    expect(result.current.store.get().bootstrap?.working_dir).toBe('/w')
    // One parse per frame: the stream validated the payload, and the store
    // takes it as it arrives instead of validating it again.
    expect(parse).toHaveBeenCalledTimes(1)
    parse.mockRestore()
    unmount()
  })

  it('unsubscribes a session it no longer wants and sends nothing for an unrelated change', async () => {
    const subscribe = vi.spyOn(StreamClient.prototype, 'subscribe')
    const unsubscribe = vi.spyOn(StreamClient.prototype, 'unsubscribe')
    const h = harness('#t=abc123', null)
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

  it('releases the data of a session it stops following', async () => {
    const h = harness('#t=abc123', null)
    const { result, unmount } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    act(() => {
      result.current.store.set({ sessions: [sessionRow('s1'), sessionRow('s2')] })
      result.current.store.selectSession('s1')
    })
    act(() =>
      result.current.store.applyFrame({
        seq: 1,
        type: 'session_snapshot',
        sessionId: 's1',
        payload: { session_id: 's1', messages: [], has_more: false, files: [], busy: false, queued: [] },
      }),
    )
    expect(result.current.store.get().sessionData.s1).toBeDefined()

    // s1 is idle and no longer selected, so the tab drops its subscription
    // and with it the page of history it kept for a stream it cannot get.
    act(() => result.current.store.selectSession('s2'))
    expect(result.current.store.get().sessionData.s1).toBeUndefined()
    unmount()
  })

  it('unsubscribes before it subscribes when the wanted set is full', async () => {
    const subscribe = vi.spyOn(StreamClient.prototype, 'subscribe')
    const unsubscribe = vi.spyOn(StreamClient.prototype, 'unsubscribe')
    const h = harness('#t=abc123', null)
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
    const h = harness('#t=abc123', null)
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
    const h = harness('#t=abc123', null)
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

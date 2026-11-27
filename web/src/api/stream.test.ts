import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StreamClient, backoffDelay } from './stream'
import type { ParsedFrame } from './wire'

class FakeSocket {
  static opened: FakeSocket[] = []
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    FakeSocket.opened.push(this)
  }
  send(data: string): void {
    this.sent.push(data)
  }
  close(): void {
    this.readyState = 3
  }
  open(): void {
    this.readyState = 1
    this.onopen?.()
  }
  receive(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
  drop(code = 1006): void {
    this.readyState = 3
    this.onclose?.({ code })
  }
}

function client(onFrame = vi.fn(), onState = vi.fn()) {
  return new StreamClient({
    url: () => 'ws://127.0.0.1:1234/api/stream',
    protocols: () => ['crush.v2', 'crush.auth.token'],
    handlers: { onFrame, onState, onBootstrap: vi.fn() },
    createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
    random: () => 1,
  })
}

const subscribeFrame = (sessionID: string) => JSON.stringify({ type: 'subscribe', session_id: sessionID })

// A bootstrap the committed wire schema accepts: the client validates every
// frame with parseFrame, so the fixture carries the fields the gateway sends.
const bootstrap = {
  version: 'x',
  working_dir: '/w',
  current_session_id: 's1',
  sessions: [],
  model: { id: 'm', name: 'n', provider: 'p', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'standard',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
}

describe('backoffDelay', () => {
  it('follows the step list and never exceeds it', () => {
    expect(backoffDelay(0, () => 1)).toBe(500)
    expect(backoffDelay(1, () => 1)).toBe(1000)
    expect(backoffDelay(5, () => 1)).toBe(10000)
    expect(backoffDelay(9, () => 1)).toBe(10000)
    expect(backoffDelay(0, () => 0)).toBe(250)
  })
})

describe('StreamClient', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeSocket.opened = []
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('connects with the token entry beside the protocol', () => {
    const s = client()
    s.start()
    expect(FakeSocket.opened).toHaveLength(1)
    expect(FakeSocket.opened[0].url).toBe('ws://127.0.0.1:1234/api/stream')
    expect(FakeSocket.opened[0].protocols).toEqual(['crush.v2', 'crush.auth.token'])
    s.stop()
  })

  it('pings every 15 seconds while open', () => {
    const s = client()
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    vi.advanceTimersByTime(15000)
    expect(socket.sent).toContain(JSON.stringify({ type: 'ping' }))
    s.stop()
  })

  it('sends subscribe and re-sends it after a reconnect', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    expect(first.sent).toContain(subscribeFrame('s1'))

    first.drop()
    vi.advanceTimersByTime(500)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(subscribeFrame('s1'))
    s.stop()
  })

  it('reconnects at once on 4001 and resubscribes on the new socket', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    first.drop(4001)
    vi.advanceTimersByTime(0)
    expect(FakeSocket.opened).toHaveLength(2)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(subscribeFrame('s1'))
    s.stop()
  })

  it('reports reconnecting, then open', () => {
    const onState = vi.fn()
    const s = client(vi.fn(), onState)
    s.start()
    FakeSocket.opened[0].open()
    FakeSocket.opened[0].drop()
    expect(onState).toHaveBeenLastCalledWith('reconnecting')
    vi.advanceTimersByTime(500)
    FakeSocket.opened[1].open()
    expect(onState).toHaveBeenLastCalledWith('open')
    s.stop()
  })

  it('passes the bootstrap payload to its handler and tracks the sequence', () => {
    const onBootstrap = vi.fn()
    const s = new StreamClient({
      url: () => 'ws://localhost/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.token'],
      handlers: { onFrame: vi.fn(), onState: vi.fn(), onBootstrap },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 7, type: 'bootstrap', payload: bootstrap })
    expect(onBootstrap).toHaveBeenCalledTimes(1)
    expect(onBootstrap.mock.calls[0][0]).toEqual(bootstrap)
    expect(s.lastSeq).toBe(7)
    s.stop()
  })

  it('counts an unknown frame type as a contract break', () => {
    const onFrame = vi.fn()
    const s = client(onFrame)
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 1, type: 'not_a_frame', payload: {} })
    expect(onFrame).not.toHaveBeenCalled()
    s.stop()
  })

  it('never opens a socket without a token entry', () => {
    const onState = vi.fn()
    const s = new StreamClient({
      url: () => 'ws://127.0.0.1:1234/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.'],
      handlers: { onFrame: vi.fn(), onState, onBootstrap: vi.fn() },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    expect(FakeSocket.opened).toHaveLength(0)
    expect(s.state).toBe('closed')
    expect(onState).not.toHaveBeenCalledWith('connecting')
    expect(onState).not.toHaveBeenCalledWith('open')
    s.stop()
  })

  it('does not reconnect once the token entry is bare', () => {
    let protocols = ['crush.v2', 'crush.auth.token']
    const s = new StreamClient({
      url: () => 'ws://127.0.0.1:1234/api/stream',
      protocols: () => protocols,
      handlers: { onFrame: vi.fn(), onState: vi.fn(), onBootstrap: vi.fn() },
      createSocket: (url, p) => new FakeSocket(url, p) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    FakeSocket.opened[0].open()
    protocols = ['crush.v2', 'crush.auth.']
    FakeSocket.opened[0].drop()
    vi.advanceTimersByTime(60000)
    expect(FakeSocket.opened).toHaveLength(1)
    expect(s.state).toBe('closed')
    s.stop()
  })

  it('drops a frame it cannot parse and warns without the payload', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const onFrame = vi.fn()
    const s = client(onFrame)
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 1, type: 'session_status', payload: { session_id: 's1', status: 'SENTINEL' } })
    socket.receive({ seq: 2, type: 'not_a_frame', payload: { secret: 'SENTINEL' } })
    expect(onFrame).not.toHaveBeenCalled()
    expect(warn).toHaveBeenCalledTimes(2)
    expect(JSON.stringify(warn.mock.calls)).not.toContain('SENTINEL')
    expect(s.lastSeq).toBe(0)
    socket.receive({ seq: 3, type: 'agent_runs', payload: [] })
    expect(onFrame).toHaveBeenCalledTimes(1)
    expect(s.lastSeq).toBe(3)
    s.stop()
  })

  it('sends a subscription made before the socket opened', () => {
    const s = client()
    s.subscribe('s1')
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    expect(socket.sent).toContain(subscribeFrame('s1'))
    s.stop()
  })

  it('sends unsubscribe and does not re-send it after a reconnect', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    s.subscribe('s2')
    s.unsubscribe('s1')
    expect(first.sent).toContain(JSON.stringify({ type: 'unsubscribe', session_id: 's1' }))
    first.drop()
    vi.advanceTimersByTime(500)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).not.toContain(subscribeFrame('s1'))
    expect(second.sent).toContain(subscribeFrame('s2'))
    s.stop()
  })

  it('subscribes once even when the same session is asked twice', () => {
    const s = client()
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    s.subscribe('s1')
    s.subscribe('s1')
    expect(socket.sent.filter((data) => data === subscribeFrame('s1'))).toHaveLength(1)
    s.stop()
  })

  it('connects without a since and resumes from the last seen frame', () => {
    const s = client()
    s.start()
    expect(FakeSocket.opened[0].url).toBe('ws://127.0.0.1:1234/api/stream')
    const first = FakeSocket.opened[0]
    first.open()
    first.receive({ seq: 0, type: 'bootstrap', payload: bootstrap })
    for (let seq = 1; seq <= 3; seq++) {
      first.receive({ seq, type: 'agent_runs', payload: [] })
    }
    expect(s.lastSeq).toBe(3)
    first.drop()
    vi.advanceTimersByTime(500)
    expect(FakeSocket.opened).toHaveLength(2)
    expect(FakeSocket.opened[1].url).toBe('ws://127.0.0.1:1234/api/stream?since=3')
    s.stop()
  })

  it('sends no since before it has seen a bootstrap', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    first.receive({ seq: 4, type: 'agent_runs', payload: [] })
    first.drop()
    vi.advanceTimersByTime(500)
    expect(FakeSocket.opened[1].url).toBe('ws://127.0.0.1:1234/api/stream')
    s.stop()
  })

  it('carries since on a 4001 reconnect and resubscribes', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    first.receive({ seq: 0, type: 'bootstrap', payload: bootstrap })
    first.receive({ seq: 5, type: 'agent_runs', payload: [] })
    first.drop(4001)
    vi.advanceTimersByTime(0)
    expect(FakeSocket.opened).toHaveLength(2)
    const second = FakeSocket.opened[1]
    expect(second.url).toBe('ws://127.0.0.1:1234/api/stream?since=5')
    second.open()
    expect(second.sent).toContain(subscribeFrame('s1'))
    s.stop()
  })

  it('keeps processing frames when a handler throws, and logs neither', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const applied: number[] = []
    const onFrame = vi.fn((frame: ParsedFrame) => {
      if (frame.seq === 1) {
        throw new Error('SENTINEL')
      }
      applied.push(frame.seq)
    })
    const onBootstrap = vi.fn(() => {
      throw new Error('SENTINEL')
    })
    const s = new StreamClient({
      url: () => 'ws://127.0.0.1:1234/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.token'],
      handlers: { onFrame, onState: vi.fn(), onBootstrap },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 1, type: 'agent_runs', payload: [] })
    socket.receive({ seq: 2, type: 'agent_runs', payload: [] })
    socket.receive({ seq: 3, type: 'bootstrap', payload: bootstrap })
    socket.receive({ seq: 4, type: 'agent_runs', payload: [] })
    expect(applied).toEqual([2, 4])
    expect(onBootstrap).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenCalledWith('web: dropping a frame the handler rejected')
    expect(JSON.stringify(warn.mock.calls)).not.toContain('SENTINEL')
    expect(s.lastSeq).toBe(4)
    s.stop()
  })

  it('takes its network listeners down on the no-token path', () => {
    const remove = vi.spyOn(window, 'removeEventListener')
    const onState = vi.fn()
    const s = new StreamClient({
      url: () => 'ws://127.0.0.1:1234/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.'],
      handlers: { onFrame: vi.fn(), onState, onBootstrap: vi.fn() },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    expect(remove).toHaveBeenCalledWith('online', expect.any(Function))
    expect(remove).toHaveBeenCalledWith('offline', expect.any(Function))
    s.stop()
  })

  it('treats a bootstrap after heavy streaming as the new watermark', () => {
    const onBootstrap = vi.fn()
    const onFrame = vi.fn()
    const onState = vi.fn()
    const s = new StreamClient({
      url: () => 'ws://127.0.0.1:1234/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.token'],
      handlers: { onFrame, onState, onBootstrap },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    for (let seq = 1; seq <= 40; seq++) {
      first.receive({ seq, type: 'agent_runs', payload: [] })
    }
    expect(s.lastSeq).toBe(40)
    // A full queue closes with 4001: the tab reconnects at once, and the
    // bootstrap re-anchors the sequence the restarted gateway counts from.
    first.drop(4001)
    vi.advanceTimersByTime(0)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(subscribeFrame('s1'))
    second.receive({ seq: 0, type: 'bootstrap', payload: bootstrap })
    expect(onBootstrap).toHaveBeenCalledTimes(1)
    expect(onBootstrap.mock.calls[0][0]).toEqual(bootstrap)
    expect(s.lastSeq).toBe(0)
    expect(onState).toHaveBeenLastCalledWith('open')
    s.stop()
  })

  it('ignores frames from a socket it left behind', () => {
    const onFrame = vi.fn()
    const s = client(onFrame)
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.stop()
    s.start()
    const second = FakeSocket.opened[1]
    second.open()
    first.receive({ seq: 9, type: 'agent_runs', payload: [] })
    expect(onFrame).not.toHaveBeenCalled()
    second.receive({ seq: 10, type: 'agent_runs', payload: [] })
    expect(onFrame).toHaveBeenCalledTimes(1)
    s.stop()
  })

  it('waits for the network instead of retrying while offline', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    window.dispatchEvent(new Event('offline'))
    first.drop()
    vi.advanceTimersByTime(60000)
    expect(FakeSocket.opened).toHaveLength(1)
    expect(s.state).toBe('reconnecting')

    window.dispatchEvent(new Event('online'))
    expect(FakeSocket.opened).toHaveLength(2)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(subscribeFrame('s1'))
    expect(s.state).toBe('open')
    s.stop()
  })

  it('stops for good and reports closed', () => {
    const onState = vi.fn()
    const s = client(vi.fn(), onState)
    s.start()
    FakeSocket.opened[0].open()
    s.stop()
    expect(s.state).toBe('closed')
    expect(onState).toHaveBeenLastCalledWith('closed')
    vi.advanceTimersByTime(60000)
    expect(FakeSocket.opened).toHaveLength(1)
  })

  it('reports connecting again on a fresh start', () => {
    const onState = vi.fn()
    const s = client(vi.fn(), onState)
    s.start()
    FakeSocket.opened[0].open()
    FakeSocket.opened[0].drop()
    s.stop()
    s.start()
    expect(onState).toHaveBeenLastCalledWith('connecting')
    s.stop()
  })
})

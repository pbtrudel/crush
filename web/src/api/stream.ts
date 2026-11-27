import { parseFrame, type ParsedFrame } from './wire'

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export const backoffSteps = [500, 1000, 2000, 4000, 8000, 10000]

const pingIntervalMs = 15000
const resyncCode = 4001
// authPrefix starts the subprotocol entry that carries the token. The
// gateway refuses a handshake without it, so a bare entry means the tab has
// no credential to offer and must not open a socket at all.
const authPrefix = 'crush.auth.'

// backoffDelay is the wait before reconnect attempt number `attempt`,
// jittered inside its step so a gateway restart does not gather every tab
// at the same instant, and capped by the last step.
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const step = backoffSteps[Math.min(attempt, backoffSteps.length - 1)]
  return Math.round(step / 2 + (random() * step) / 2)
}

export interface StreamHandlers {
  onBootstrap(payload: unknown): void
  onFrame(frame: ParsedFrame): void
  onState(state: ConnectionState): void
  // onClose reports every close with the code the browser gave and whether
  // the socket ever opened. A refused handshake is the one close the state
  // cannot describe: the browser hides the gateway's 401 behind code 1006,
  // and the move to 'reconnecting' happens only once per streak.
  onClose?(code: number, opened: boolean): void
}

export interface StreamOptions {
  url: () => string
  protocols: () => string[]
  handlers: StreamHandlers
  createSocket?: (url: string, protocols: string[]) => WebSocket
  random?: () => number
  pingIntervalMs?: number
}

// hasAuthToken reports whether the offered subprotocols carry a token. The
// token itself is never read, logged or stored here.
function hasAuthToken(protocols: string[]): boolean {
  return protocols.some((entry) => entry.startsWith(authPrefix) && entry.length > authPrefix.length)
}

// offline reports whether the browser has no network. A tab that loads from
// the back/forward cache while offline must wait for the online event rather
// than spin on sockets that cannot open.
function offline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false
}

export class StreamClient {
  private socket: WebSocket | null = null
  private attempt = 0
  private reconnectTimer: number | null = null
  private pingTimer: number | null = null
  private stopped = true
  private isOffline = false
  private hasBootstrap = false
  private readonly subscriptions = new Set<string>()
  private seq = 0
  private currentState: ConnectionState = 'closed'

  constructor(private readonly options: StreamOptions) {}

  get state(): ConnectionState {
    return this.currentState
  }

  get lastSeq(): number {
    return this.seq
  }

  start(): void {
    if (!this.stopped) {
      return
    }
    this.stopped = false
    this.isOffline = offline()
    window.addEventListener('online', this.handleOnline)
    window.addEventListener('offline', this.handleOffline)
    this.connect()
  }

  stop(): void {
    this.stopped = true
    this.attempt = 0
    this.clearTimers()
    window.removeEventListener('online', this.handleOnline)
    window.removeEventListener('offline', this.handleOffline)
    this.setState('closed')
    const socket = this.socket
    this.socket = null
    socket?.close()
  }

  subscribe(sessionID: string): void {
    if (this.subscriptions.has(sessionID)) {
      return
    }
    this.subscriptions.add(sessionID)
    this.send({ type: 'subscribe', session_id: sessionID })
  }

  unsubscribe(sessionID: string): void {
    if (!this.subscriptions.delete(sessionID)) {
      return
    }
    this.send({ type: 'unsubscribe', session_id: sessionID })
  }

  private readonly handleOnline = (): void => {
    this.isOffline = false
    if (this.stopped || this.socket !== null) {
      return
    }
    this.clearReconnect()
    this.connect()
  }

  private readonly handleOffline = (): void => {
    this.isOffline = true
    // The online event asks for a reconnect, so a retry scheduled now would
    // only fail and spend the backoff steps.
    this.clearReconnect()
  }

  private connect(): void {
    // A socket without the token entry is refused by the gateway before the
    // handshake completes, so the client stops here, listeners and all, and
    // waits to be started again once a token arrives.
    if (!hasAuthToken(this.options.protocols())) {
      this.stop()
      return
    }
    this.setState(this.attempt === 0 ? 'connecting' : 'reconnecting')
    if (this.isOffline) {
      // The online event asks for a connect again; a socket opened while the
      // browser reports no network would only fail.
      return
    }
    const socket = (this.options.createSocket ?? ((url, protocols) => new WebSocket(url, protocols)))(
      this.resumeURL(),
      this.options.protocols(),
    )
    this.socket = socket
    // opened is the socket's own history, not the client's state: a stale
    // socket must never look like a handshake that succeeded.
    let opened = false
    socket.onopen = () => {
      if (this.socket !== socket) {
        return
      }
      opened = true
      this.attempt = 0
      this.setState('open')
      this.startPing()
      // Every subscription is re-sent on open: a reconnect arrives on a
      // connection that holds none, and replay never carries per-session
      // frames, so re-subscribing is what restores them.
      for (const sessionID of this.subscriptions) {
        this.send({ type: 'subscribe', session_id: sessionID })
      }
    }
    socket.onmessage = (event) => {
      if (this.socket !== socket) {
        return
      }
      this.handleMessage(event.data)
    }
    socket.onclose = (event) => {
      if (this.socket !== socket) {
        return
      }
      this.handleClose(event.code, opened)
    }
  }

  private handleClose(code: number, opened: boolean): void {
    this.stopPing()
    this.socket = null
    if (this.stopped) {
      this.setState('closed')
      return
    }
    // A resync close means the ring could not answer: reconnect at once and
    // let the subscriptions rebuild the session state from fresh snapshots.
    const delay = code === resyncCode ? 0 : backoffDelay(this.attempt, this.options.random)
    this.attempt += 1
    this.setState('reconnecting')
    // The close handler runs after the state move, so a listener sees a
    // consistent client, and before the retry is scheduled, so a listener
    // that stops the client cancels the next attempt.
    this.deliverClose(code, opened)
    if (this.stopped) {
      this.setState('closed')
      return
    }
    this.clearReconnect()
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== 'string') {
      return
    }
    let frame: ParsedFrame
    try {
      frame = parseFrame(data)
    } catch {
      // The frame is dropped, not repaired: an unknown type is a contract
      // break. Neither the frame nor the error is logged, because both can
      // carry the session's content.
      console.warn('web: dropping a frame the client cannot read')
      return
    }
    if (frame.type === 'bootstrap') {
      // The bootstrap is the first frame of a fresh connection and carries
      // the gateway's current sequence, so it re-anchors the watermark even
      // when a restarted gateway counts from zero again. A bootstrap the
      // handler could not apply is not seen: the tab still has nothing, so
      // the next connect asks for one again instead of resuming.
      if (this.deliver(() => this.options.handlers.onBootstrap(frame.payload))) {
        this.seq = frame.seq
        this.hasBootstrap = true
      }
      return
    }
    if (frame.seq > this.seq) {
      this.seq = frame.seq
    }
    this.deliver(() => this.options.handlers.onFrame(frame))
  }

  // deliver runs one handler and contains it: a handler that throws must not
  // stop the frames behind it. Nothing about the failure is logged, because
  // neither the frame nor the error it raised may reach the console. It
  // reports whether the handler accepted the frame.
  private deliver(call: () => void): boolean {
    try {
      call()
      return true
    } catch {
      console.warn('web: dropping a frame the handler rejected')
      return false
    }
  }

  // deliverClose runs the optional close handler and contains it: a listener
  // that throws must not stop the reconnect.
  private deliverClose(code: number, opened: boolean): void {
    const handler = this.options.handlers.onClose
    if (handler === undefined) {
      return
    }
    try {
      handler(code, opened)
    } catch {
      console.warn('web: dropping a close the handler rejected')
    }
  }

  // resumeURL is the socket address with the resume point attached. The value
  // is this client's own watermark: a caller that sent since=0 on its first
  // connect would replay the ring and lose the bootstrap that starts it.
  private resumeURL(): string {
    const url = this.options.url()
    if (!this.hasBootstrap) {
      return url
    }
    return `${url}${url.includes('?') ? '&' : '?'}since=${this.seq}`
  }

  private send(message: unknown): void {
    if (this.socket === null || this.socket.readyState !== 1) {
      return
    }
    this.socket.send(JSON.stringify(message))
  }

  private startPing(): void {
    this.stopPing()
    const interval = this.options.pingIntervalMs ?? pingIntervalMs
    this.pingTimer = window.setInterval(() => this.send({ type: 'ping' }), interval)
  }

  private stopPing(): void {
    if (this.pingTimer !== null) {
      window.clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }

  private clearReconnect(): void {
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }

  private clearTimers(): void {
    this.stopPing()
    this.clearReconnect()
  }

  private setState(state: ConnectionState): void {
    if (this.currentState === state) {
      return
    }
    this.currentState = state
    this.options.handlers.onState(state)
  }
}

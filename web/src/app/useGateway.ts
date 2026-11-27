import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { ApiClient, stripTokenFromUrl } from '../api/http'
import { StreamClient } from '../api/stream'
import { bootstrapSchema } from '../api/wire'
import { createAppStore, wantedSubscriptions } from '../store/app'
import type { AppState, AppStore } from '../store/app'
import type { Store } from '../store/store'
import { boot, isUnauthorized } from './boot'
import { safeStorage } from './storage'

export type BootState = 'authenticating' | 'ready' | 'unauthorized' | 'unreachable'

// GatewayClient is the part of ApiClient the tab needs before anything is
// rendered. It is what a test substitutes, so the hook never reaches the
// network on its own. The response body of get is unused — the call exists to
// prove the gateway answers.
export interface GatewayClient {
  readonly authenticated: boolean
  authenticate(token: string): Promise<void>
  get(path: string): Promise<unknown>
  wsProtocols(): string[]
  clearToken(): void
}

export interface GatewayOptions {
  client?: GatewayClient
  hash?: string
  stripHash?: () => void
  createSocket?: (url: string, protocols: string[]) => WebSocket
  // storage carries the unread marks. The token has its own store and stays
  // in session storage.
  storage?: Storage
}

export interface Gateway {
  state: BootState
  error: string | null
  client: ApiClient
  stream: StreamClient
  store: Store<AppState> & AppStore
}

// useGateway owns the tab's connection: it authenticates once, then runs the
// stream for the life of the tab. The bootstrap frame is what fills the
// store, so the shell renders before any session is selected.
export function useGateway(options: GatewayOptions = {}): Gateway {
  const client = useMemo(
    () => (options.client as ApiClient | undefined) ?? new ApiClient({ storage: safeStorage('session') }),
    [options.client],
  )
  const store = useMemo(() => createAppStore(options.storage ?? safeStorage('local')), [options.storage])
  const [state, setState] = useState<BootState>('authenticating')
  const [error, setError] = useState<string | null>(null)
  // The stream's handlers need the stream itself, which is created below, so
  // they reach it through a ref.
  const streamRef = useRef<StreamClient | null>(null)
  const alive = useRef(true)
  const probing = useRef(false)

  // probeRefusedHandshake asks the REST API why the socket dropped. A refused
  // handshake reaches the browser as close code 1006 with the gateway's 401
  // hidden, so a socket that never opened is the only hint of a stale token,
  // and /api/health is where the status is visible. One probe per
  // reconnection streak: the stream reports the move to 'reconnecting' once,
  // and the flag keeps an overlapping probe out.
  const probeRefusedHandshake = useCallback(async () => {
    if (probing.current) {
      return
    }
    probing.current = true
    try {
      await client.get('/api/health')
    } catch (cause: unknown) {
      if (!isUnauthorized(cause)) {
        // The gateway is unreachable or answered something else: the token
        // is not the problem, so the stream keeps retrying.
        return
      }
      // The gateway answers and the token is refused: forget the token, stop
      // the backoff loop and ask for a fresh link.
      client.clearToken()
      streamRef.current?.stop()
      if (alive.current) {
        setState('unauthorized')
      }
    } finally {
      probing.current = false
    }
  }, [client])

  const stream = useMemo(() => {
    const created = new StreamClient({
      // The stream appends its own resume point, so this is the bare socket
      // address.
      url: () => `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/stream`,
      protocols: () => client.wsProtocols(),
      handlers: {
        onBootstrap: (payload) => store.applyBootstrap(bootstrapSchema.parse(payload)),
        onFrame: (frame) => store.applyFrame(frame),
        onState: (next) => {
          store.setConnection(next)
          if (next === 'reconnecting') {
            void probeRefusedHandshake()
          }
        },
      },
      createSocket: options.createSocket,
    })
    streamRef.current = created
    return created
  }, [client, store, options.createSocket, probeRefusedHandshake])

  // The launch fragment is a boot-time input, so it is read once: stripping
  // the token from the address bar must not start boot a second time.
  const hash = useMemo(() => options.hash ?? window.location.hash, [options.hash])
  // The default stripper replaces the address bar entry. It is memoised
  // because a fresh function on every render would restart boot, and with it
  // the stream.
  const stripHash = useMemo(
    () => options.stripHash ?? (() => stripTokenFromUrl(window.location, window.history)),
    [options.stripHash],
  )

  // A probe can resolve after the tab is gone; the ref keeps it from setting
  // state on an unmounted hook. StrictMode's second mount sets it back.
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    let mounted = true
    void boot({ client, hash, stripHash }).then((result) => {
      if (!mounted) {
        return
      }
      if (!result.ok) {
        setState(result.reason)
        setError(result.message)
        return
      }
      stream.start()
      setState('ready')
    })
    return () => {
      // StrictMode runs an effect twice: the first start is stopped here
      // before the second one runs, so the tab never holds two sockets.
      mounted = false
      stream.stop()
    }
  }, [client, stream, hash, stripHash])

  // The subscriptions follow the store: the selected session plus every
  // session that is not idle. Diffing against what is already subscribed
  // keeps an unrelated store change from sending anything.
  useEffect(() => {
    const subscribed = new Set<string>()
    const reconcile = () => {
      const wanted = wantedSubscriptions(store.get())
      for (const sessionID of wanted) {
        if (!subscribed.has(sessionID)) {
          subscribed.add(sessionID)
          stream.subscribe(sessionID)
        }
      }
      for (const sessionID of subscribed) {
        if (!wanted.includes(sessionID)) {
          subscribed.delete(sessionID)
          stream.unsubscribe(sessionID)
        }
      }
    }
    reconcile()
    const unsubscribe = store.subscribe(reconcile)
    return () => {
      unsubscribe()
      for (const sessionID of subscribed) {
        stream.unsubscribe(sessionID)
      }
    }
  }, [store, stream])

  return { state, error, client, stream, store }
}

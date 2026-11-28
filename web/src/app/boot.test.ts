import { describe, expect, it, vi } from 'vitest'

import { ApiClient } from '../api/http'
import { boot } from './boot'
import { memoryStorage } from './storage'

// Reply answers one request for the gateway stub: the response an ApiClient
// reads, or a rejection that never reached a gateway.
type Reply = () => Promise<Response>

const ok: Reply = async () => new Response(JSON.stringify({ ok: true }), { status: 200 })
const noContent: Reply = async () => new Response(null, { status: 204 })
const refused: Reply = async () =>
  new Response(JSON.stringify({ code: 'unauthorized', error: 'missing or invalid token' }), { status: 401 })
const offline: Reply = async () => Promise.reject(new Error('offline'))

interface Request {
  path: string
  init?: RequestInit
}

interface EnvOptions {
  // storedToken is the credential the tab already holds, or null for none.
  storedToken?: string | null
  hash?: string
  authenticate?: Reply
  health?: Reply
}

// env builds a boot environment around a real ApiClient: only its fetch is
// replaced, so the test drives the client the tab boots with instead of a
// stand-in that can drift from the class.
function env(options: EnvOptions = {}) {
  const { storedToken = null, hash = '', authenticate = noContent, health = ok } = options
  const storage = memoryStorage(storedToken === null ? {} : { 'crush.token': storedToken })
  const requests: Request[] = []
  const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = String(input)
    requests.push({ path, init })
    if (path === '/api/auth') {
      return authenticate()
    }
    if (path === '/api/health') {
      return health()
    }
    return new Response('{}', { status: 404 })
  })
  const stripHash = vi.fn()
  const client = new ApiClient({ storage, fetchImpl })
  return {
    client,
    storage,
    requests,
    fetchImpl,
    stripHash,
    // token is the credential the tab would send next.
    token: () => storage.getItem('crush.token'),
    boot: () => boot({ client, hash, stripHash }),
  }
}

describe('boot', () => {
  it('exchanges the hash token, strips it and checks the gateway', async () => {
    const e = env({ hash: '#t=abc123' })
    await expect(e.boot()).resolves.toEqual({ ok: true })
    expect(e.requests.map((request) => request.path)).toEqual(['/api/auth', '/api/health'])
    expect(e.requests[0].init?.body).toBe(JSON.stringify({ token: 'abc123' }))
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.token()).toBe('abc123')
  })

  it('keeps a stored token and skips the exchange', async () => {
    const e = env({ storedToken: 'stored' })
    await expect(e.boot()).resolves.toEqual({ ok: true })
    expect(e.requests.map((request) => request.path)).toEqual(['/api/health'])
    expect(e.token()).toBe('stored')
  })

  it('shows the unauthorized screen without a token', async () => {
    const e = env()
    await expect(e.boot()).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
    expect(e.requests).toHaveLength(0)
  })

  it('strips the fragment before the token is exchanged', async () => {
    const e = env({ hash: '#t=abc123' })
    await expect(e.boot()).resolves.toEqual({ ok: true })
    expect(e.stripHash.mock.invocationCallOrder[0]).toBeLessThan(e.fetchImpl.mock.invocationCallOrder[0])
  })

  it('treats a refused token as unauthorized', async () => {
    const e = env({ hash: '#t=bad', authenticate: refused })
    await expect(e.boot()).resolves.toMatchObject({
      ok: false,
      reason: 'unauthorized',
      message: 'missing or invalid token',
    })
    // The token left the address bar as soon as it was read, so the tab
    // asks for a fresh link rather than holding a fragment it cannot use.
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.token()).toBeNull()
  })

  it('reports an unreachable gateway when the exchange cannot reach it', async () => {
    const e = env({ hash: '#t=abc123', authenticate: offline })
    await expect(e.boot()).resolves.toMatchObject({ ok: false, reason: 'unreachable', message: 'offline' })
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.token()).toBeNull()
  })

  it('reports an unreachable gateway without dropping the token', async () => {
    const e = env({ storedToken: 'stored', health: offline })
    await expect(e.boot()).resolves.toMatchObject({ ok: false, reason: 'unreachable', message: 'offline' })
    expect(e.token()).toBe('stored')
  })

  it('forgets a hash token the gateway refuses', async () => {
    const e = env({ hash: '#t=stale', authenticate: refused })
    await expect(e.boot()).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.token()).toBeNull()
  })

  it('forgets a stored token the gateway refuses instead of reporting an outage', async () => {
    const e = env({ storedToken: 'stored', health: refused })
    await expect(e.boot()).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
    expect(e.token()).toBeNull()
  })
})

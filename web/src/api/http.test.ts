import { describe, expect, it, vi } from 'vitest'

import { memoryStorage } from '../app/storage'
import { ApiClient, HttpError, readTokenFromHash, stripTokenFromUrl } from './http'

describe('readTokenFromHash', () => {
  it('reads the launch token', () => {
    expect(readTokenFromHash('#t=abc123')).toBe('abc123')
  })

  it('reads it beside another fragment field', () => {
    expect(readTokenFromHash('#panel=files&t=abc123')).toBe('abc123')
  })

  it('answers null when there is no token', () => {
    expect(readTokenFromHash('')).toBeNull()
    expect(readTokenFromHash('#panel=files')).toBeNull()
  })

  it('answers null for a malformed escape, so boot does not throw', () => {
    expect(readTokenFromHash('#t=%E0%A4%A')).toBeNull()
  })
})

describe('stripTokenFromUrl', () => {
  it('keeps the path and the query', () => {
    const history = { replaceState: vi.fn() } as unknown as History
    stripTokenFromUrl({ pathname: '/sessions/s1', search: '?x=1' } as Location, history)
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/sessions/s1?x=1')
  })
})

describe('ApiClient', () => {
  it('exchanges a token and keeps it in the storage it was given', async () => {
    const storage = memoryStorage()
    const fetchImpl = vi.fn(async () => new Response(null, { status: 204 }))
    const client = new ApiClient({ storage, fetchImpl })
    await client.authenticate('abc123')
    expect(fetchImpl).toHaveBeenCalledWith('/api/auth', expect.objectContaining({ method: 'POST' }))
    expect(storage.getItem('crush.token')).toBe('abc123')
    expect(client.authenticated).toBe(true)
    expect(client.wsProtocols()).toEqual(['crush.v2', 'crush.auth.abc123'])
  })

  it('refuses a token the gateway rejects', async () => {
    const fetchImpl = vi.fn(async () =>
      new Response(JSON.stringify({ error: 'invalid token', code: 'unauthorized' }), { status: 401 }),
    )
    const client = new ApiClient({ storage: memoryStorage(), fetchImpl })
    await expect(client.authenticate('nope')).rejects.toBeInstanceOf(HttpError)
    expect(client.authenticated).toBe(false)
  })

  it('sends the bearer header and parses JSON', async () => {
    const storage = memoryStorage()
    const fetchImpl = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify({ ok: true }), { status: 200 }),
    )
    const client = new ApiClient({ storage, fetchImpl })
    client.setToken('abc123')
    await expect(client.get<{ ok: boolean }>('/api/health')).resolves.toEqual({ ok: true })
    const [, init] = fetchImpl.mock.calls[0]
    expect(init?.headers).toMatchObject({ Authorization: 'Bearer abc123' })
  })

  it('answers 204 with undefined, not a parse error', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () => new Response(null, { status: 204 })),
    })
    client.setToken('abc123')
    await expect(client.send('DELETE', '/api/sessions/s1')).resolves.toBeUndefined()
  })

  it('answers 202 with an empty body too, which is what follow does', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () => new Response('', { status: 202 })),
    })
    client.setToken('abc123')
    await expect(client.send('POST', '/api/follow', { session_id: 's1' })).resolves.toBeUndefined()
  })

  it('turns an error envelope into an HttpError', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () =>
        new Response(JSON.stringify({ error: 'Crush is working in this session', code: 'session_busy' }), { status: 409 }),
      ),
    })
    client.setToken('abc123')
    await expect(client.send('DELETE', '/api/sessions/s1')).rejects.toMatchObject({
      status: 409,
      code: 'session_busy',
      message: 'Crush is working in this session',
    })
  })

  it('never puts the token in an error message', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () => new Response('<html>nope</html>', { status: 500 })),
    })
    client.setToken('secret-token')
    const error = await client.send('GET', '/api/health').then(
      () => null,
      (err: HttpError) => err,
    )
    expect(error).toBeInstanceOf(HttpError)
    expect(error?.message).not.toContain('secret-token')
    expect(error?.message).toBe('the gateway answered 500')
  })

  it('refuses to send without a token', async () => {
    const fetchImpl = vi.fn(async () => new Response(null, { status: 204 }))
    const client = new ApiClient({ storage: memoryStorage(), fetchImpl })
    await expect(client.get('/api/health')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(fetchImpl).not.toHaveBeenCalled()
  })

  it('calls the default fetch as a global, not as a client method', async () => {
    // A window method called as a property of another object is refused by
    // the browser with "Illegal invocation", which the jsdom stub never
    // showed: the browser suite found it on the first boot.
    const receivers: unknown[] = []
    vi.stubGlobal('fetch', function (this: unknown) {
      receivers.push(this)
      return Promise.resolve(new Response('{}', { status: 200 }))
    })
    try {
      const client = new ApiClient({ storage: memoryStorage() })
      client.setToken('abc123')
      await expect(client.get('/api/health')).resolves.toEqual({})
    } finally {
      vi.unstubAllGlobals()
    }
    expect(receivers).toEqual([globalThis])
  })

  it('forgets the token on clearToken', () => {
    const storage = memoryStorage()
    const client = new ApiClient({ storage })
    client.setToken('abc123')
    client.clearToken()
    expect(client.authenticated).toBe(false)
    expect(storage.getItem('crush.token')).toBeNull()
    expect(client.wsProtocols()).toEqual(['crush.v2', 'crush.auth.'])
  })
})

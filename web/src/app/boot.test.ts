import { describe, expect, it, vi } from 'vitest'

import { HttpError } from '../api/http'
import { boot } from './boot'

function env(overrides: Partial<Parameters<typeof boot>[0]> = {}) {
  return {
    client: {
      authenticated: false,
      authenticate: vi.fn(async () => undefined),
      // Typed as the generic it stands in for, so the mock is assignable.
      get: vi.fn(async (_path: string): Promise<unknown> => ({ ok: true })),
      clearToken: vi.fn(),
    },
    hash: '',
    stripHash: vi.fn(),
    ...overrides,
  }
}

describe('boot', () => {
  it('exchanges the hash token, strips it and checks the gateway', async () => {
    const e = env({ hash: '#t=abc123' })
    await expect(boot(e)).resolves.toEqual({ ok: true })
    expect(e.client.authenticate).toHaveBeenCalledWith('abc123')
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.client.get).toHaveBeenCalledWith('/api/health')
  })

  it('keeps a stored token and skips the exchange', async () => {
    const e = env({ client: { ...env().client, authenticated: true } })
    await expect(boot(e)).resolves.toEqual({ ok: true })
    expect(e.client.authenticate).not.toHaveBeenCalled()
  })

  it('shows the unauthorized screen without a token', async () => {
    await expect(boot(env())).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
  })

  it('strips the fragment before the token is exchanged', async () => {
    const stripHash = vi.fn()
    const authenticate = vi.fn(async () => undefined)
    const e = env({ hash: '#t=abc123', stripHash, client: { ...env().client, authenticate } })
    await expect(boot(e)).resolves.toEqual({ ok: true })
    expect(stripHash.mock.invocationCallOrder[0]).toBeLessThan(authenticate.mock.invocationCallOrder[0])
  })

  it('treats a refused token as unauthorized', async () => {
    const e = env({
      hash: '#t=bad',
      client: {
        ...env().client,
        authenticate: vi.fn(async () =>
          Promise.reject(new HttpError(401, 'unauthorized', 'missing or invalid token')),
        ),
      },
    })
    await expect(boot(e)).resolves.toMatchObject({
      ok: false,
      reason: 'unauthorized',
      message: 'missing or invalid token',
    })
    // The token left the address bar as soon as it was read, so the tab
    // asks for a fresh link rather than holding a fragment it cannot use.
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.client.clearToken).toHaveBeenCalled()
  })

  it('reports an unreachable gateway when the exchange cannot reach it', async () => {
    const e = env({
      hash: '#t=abc123',
      client: { ...env().client, authenticate: vi.fn(async () => Promise.reject(new Error('offline'))) },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unreachable', message: 'offline' })
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.client.clearToken).not.toHaveBeenCalled()
  })

  it('reports an unreachable gateway without dropping the token', async () => {
    const e = env({
      client: { ...env().client, authenticated: true, get: vi.fn(async () => Promise.reject(new Error('offline'))) },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unreachable', message: 'offline' })
    expect(e.client.clearToken).not.toHaveBeenCalled()
  })

  it('forgets a hash token the gateway refuses', async () => {
    const e = env({
      hash: '#t=stale',
      client: {
        ...env().client,
        authenticate: vi.fn(async () =>
          Promise.reject(new HttpError(401, 'unauthorized', 'missing or invalid token')),
        ),
      },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.client.clearToken).toHaveBeenCalled()
  })

  it('forgets a stored token the gateway refuses instead of reporting an outage', async () => {
    const e = env({
      client: {
        ...env().client,
        authenticated: true,
        get: vi.fn(async () => Promise.reject(new HttpError(401, 'unauthorized', 'missing or invalid token'))),
      },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
    expect(e.client.clearToken).toHaveBeenCalled()
  })
})

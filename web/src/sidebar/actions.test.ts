import { describe, expect, expectTypeOf, it, vi } from 'vitest'

import type { ApiClient } from '../api/http'
import { sessionActions } from './actions'

function client() {
  return { send: vi.fn(async () => ({ id: 's2', title: 'New' })) } as unknown as ApiClient
}

describe('sessionActions', () => {
  it('creates a session with the default title', async () => {
    const c = client()
    const actions = sessionActions(c)
    await actions.create()
    expect(c.send).toHaveBeenCalledWith('POST', '/api/sessions', { title: 'New Session' })
  })

  it('renames only the title', async () => {
    const c = client()
    await sessionActions(c).rename('s2', 'Renamed')
    expect(c.send).toHaveBeenCalledWith('PUT', '/api/sessions/s2', { title: 'Renamed' })
  })

  it('deletes a session', async () => {
    const c = client()
    await sessionActions(c).remove('s2')
    expect(c.send).toHaveBeenCalledWith('DELETE', '/api/sessions/s2')
  })

  it('answers the created session, not a full row', async () => {
    const created = await sessionActions(client()).create()
    // Compile-time pin: the create route answers the session entity, and the
    // caller only reads its ID, so the action must not pretend to return a
    // SessionRow whose status it never received.
    expectTypeOf(created).toEqualTypeOf<{ id: string }>()
  })

  it('asks the terminal to open a session', async () => {
    const c = client()
    await sessionActions(c).follow('s2')
    expect(c.send).toHaveBeenCalledWith('POST', '/api/follow', { session_id: 's2' })
  })
})

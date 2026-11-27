import { HttpError, readTokenFromHash } from '../api/http'

export interface BootEnv {
  client: {
    authenticated: boolean
    authenticate(token: string): Promise<void>
    // The body is unused: the call exists to prove the gateway answers.
    get(path: string): Promise<unknown>
    // clearToken forgets a credential the gateway refused, so a reload asks
    // for a fresh link instead of offering the same dead token again.
    clearToken?(): void
  }
  hash: string
  stripHash(): void
}

export type BootResult =
  | { ok: true }
  | { ok: false; reason: 'unauthorized' | 'unreachable'; message: string }

// boot turns a launch link into an authenticated tab: exchange the token in
// the fragment, forget the fragment, then prove the gateway answers.
export async function boot(env: BootEnv): Promise<BootResult> {
  const fromHash = readTokenFromHash(env.hash)
  if (fromHash !== null) {
    try {
      await env.client.authenticate(fromHash)
    } catch (error) {
      forgetRefusedToken(env, error)
      return { ok: false, reason: 'unauthorized', message: messageOf(error) }
    }
    env.stripHash()
  } else if (!env.client.authenticated) {
    return { ok: false, reason: 'unauthorized', message: 'This tab has no token.' }
  }
  try {
    await env.client.get('/api/health')
  } catch (error) {
    // A stored token the gateway refuses is stale, not an outage: every
    // gateway start mints a new one, so the tab drops it and asks for a
    // fresh link.
    if (isUnauthorized(error)) {
      env.client.clearToken?.()
      return { ok: false, reason: 'unauthorized', message: messageOf(error) }
    }
    return { ok: false, reason: 'unreachable', message: messageOf(error) }
  }
  return { ok: true }
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof HttpError && error.status === 401
}

// forgetRefusedToken drops the stored credential only when the gateway said
// it is invalid: an unreachable gateway leaves the token in place.
function forgetRefusedToken(env: BootEnv, error: unknown): void {
  if (isUnauthorized(error)) {
    env.client.clearToken?.()
  }
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

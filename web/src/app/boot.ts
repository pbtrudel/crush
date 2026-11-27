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

// isUnauthorized reports whether the gateway refused the credential. Any
// other failure is an outage: the token may be perfectly good.
export function isUnauthorized(error: unknown): boolean {
  return error instanceof HttpError && error.status === 401
}

// boot turns a launch link into an authenticated tab: read the token in the
// fragment, forget the fragment, exchange the token, then prove the gateway
// answers.
export async function boot(env: BootEnv): Promise<BootResult> {
  const fromHash = readTokenFromHash(env.hash)
  if (fromHash !== null) {
    // The fragment is stripped the moment it is read, not after it is
    // accepted: a token that stays in the address bar outlives its use and
    // lands in the history, and a failed exchange would leave it there.
    env.stripHash()
    try {
      await env.client.authenticate(fromHash)
    } catch (error) {
      // Only a refusal is a token problem. A request that never reached the
      // gateway leaves the stored token alone: it may still be good.
      if (isUnauthorized(error)) {
        env.client.clearToken?.()
        return { ok: false, reason: 'unauthorized', message: messageOf(error) }
      }
      return { ok: false, reason: 'unreachable', message: messageOf(error) }
    }
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

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

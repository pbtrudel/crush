import type { ApiClient } from '../api/http'
import type { SessionRow } from '../api/wire'

// SessionActions is the session lifecycle the sidebar offers. Each call is
// one request; the store learns about the result from the gateway's frames,
// except for a delete, which the caller forgets at once.
export interface SessionActions {
  create(title?: string): Promise<SessionRow>
  rename(id: string, title: string): Promise<void>
  remove(id: string): Promise<void>
  follow(id: string): Promise<void>
}

export function sessionActions(client: ApiClient): SessionActions {
  return {
    create: (title = 'New Session') => client.send<SessionRow>('POST', '/api/sessions', { title }),
    rename: async (id, title) => {
      // A rename carries the title alone: the gateway keeps everything else.
      await client.send('PUT', `/api/sessions/${encodeURIComponent(id)}`, { title })
    },
    remove: async (id) => {
      await client.send('DELETE', `/api/sessions/${encodeURIComponent(id)}`)
    },
    follow: async (id) => {
      // The terminal switches to the session; the tab keeps its own
      // selection, which the gateway reports back with a bootstrap.
      await client.send('POST', '/api/follow', { session_id: id })
    },
  }
}

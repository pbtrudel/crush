import type { ApiClient } from '../api/http'

// CreatedSession is what the create route answers: the session entity, whose
// status and message count only arrive later on the stream. The caller needs
// the ID and nothing else, so it is not typed as a SessionRow it never got.
export interface CreatedSession {
  id: string
}

// SessionActions is the session lifecycle the sidebar offers. Each call is
// one request; the store learns about the result from the gateway's frames,
// except for a delete, which the caller forgets at once.
export interface SessionActions {
  create(title?: string): Promise<CreatedSession>
  rename(id: string, title: string): Promise<void>
  remove(id: string): Promise<void>
  follow(id: string): Promise<void>
}

export function sessionActions(client: ApiClient): SessionActions {
  return {
    create: (title = 'New Session') => client.send<CreatedSession>('POST', '/api/sessions', { title }),
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

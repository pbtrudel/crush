const tokenKey = 'crush.token'

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'HttpError'
  }
}

export interface ApiClientOptions {
  fetchImpl?: typeof fetch
  storage?: Storage
}

export function readTokenFromHash(hash: string): string | null {
  const match = /(?:^#|&)t=([^&]+)/.exec(hash)
  if (!match) {
    return null
  }
  try {
    return decodeURIComponent(match[1])
  } catch {
    // A malformed fragment is not a token: the tab shows the unauthorized
    // screen instead of throwing on boot.
    return null
  }
}

export function stripTokenFromUrl(location: Location, history: History): void {
  history.replaceState(null, '', location.pathname + location.search)
}

export class ApiClient {
  private token: string | null

  constructor(private readonly options: ApiClientOptions = {}) {
    this.token = this.storage.getItem(tokenKey)
  }

  private get storage(): Storage {
    return this.options.storage ?? sessionStorage
  }

  private get fetchImpl(): typeof fetch {
    return this.options.fetchImpl ?? fetch
  }

  get authenticated(): boolean {
    return this.token !== null
  }

  setToken(token: string): void {
    this.token = token
    this.storage.setItem(tokenKey, token)
  }

  clearToken(): void {
    this.token = null
    this.storage.removeItem(tokenKey)
  }

  // authenticate exchanges the launch token for the session's credential.
  // The gateway answers 204 and never sets a cookie: the tab keeps the token
  // and sends it as a bearer header.
  async authenticate(token: string): Promise<void> {
    const response = await this.fetchImpl('/api/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    })
    if (!response.ok) {
      throw await toHttpError(response)
    }
    this.setToken(token)
  }

  async get<T>(path: string): Promise<T> {
    return this.send<T>('GET', path)
  }

  async send<T>(method: string, path: string, body?: unknown): Promise<T> {
    if (this.token === null) {
      throw new HttpError(401, 'unauthorized', 'the tab has no token')
    }
    const headers: Record<string, string> = { Authorization: `Bearer ${this.token}` }
    const init: RequestInit = { method, headers }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      init.body = JSON.stringify(body)
    }
    const response = await this.fetchImpl(path, init)
    if (!response.ok) {
      throw await toHttpError(response)
    }
    // Several routes answer 202 or 204 with no body (follow, cancel, queue
    // clear, detach), so the body is read as text and parsed only when it is
    // there.
    const text = await response.text()
    if (text === '') {
      return undefined as T
    }
    return JSON.parse(text) as T
  }

  // wsProtocols are the entries the stream offers: the token entry beside the
  // protocol, because a browser cannot set an Authorization header there.
  wsProtocols(): string[] {
    return ['crush.v2', `crush.auth.${this.token ?? ''}`]
  }
}

// toHttpError builds the error a failed response throws. The message is the
// gateway's fixed sentence, or a generic one when the body is unreadable: the
// request and its token never reach it.
async function toHttpError(response: Response): Promise<HttpError> {
  const fallback = `the gateway answered ${response.status}`
  try {
    const body = (await response.json()) as { error?: string; code?: string }
    return new HttpError(response.status, body.code ?? 'unknown', body.error ?? fallback)
  } catch {
    return new HttpError(response.status, 'unknown', fallback)
  }
}

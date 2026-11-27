export type StorageKind = 'local' | 'session'

// memoryStorage is an in-memory Storage. It backs the unit tests, and a
// browser that refuses its own storage keeps the tab's token and preferences
// here for the life of the page instead of failing to boot.
export function memoryStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial))
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (key: string) => data.get(key) ?? null,
    key: (index: number) => [...data.keys()][index] ?? null,
    removeItem: (key: string) => data.delete(key),
    setItem: (key: string, value: string) => data.set(key, value),
  }
}

// One stand-in per kind: a token the client wrote is the token the next boot
// reads, and a theme preference written in the frame is the one the next
// render reads, even though neither outlives the page.
const fallbacks = new Map<StorageKind, Storage>()

// safeStorage answers the browser storage of the given kind, or an in-memory
// stand-in when the browser blocks site data. Accessing window.localStorage
// in a browser that blocks it throws a SecurityError, and under Node it is
// simply absent, so both are handled here rather than in every caller.
export function safeStorage(kind: StorageKind): Storage {
  try {
    const storage = kind === 'local' ? window.localStorage : window.sessionStorage
    if (storage) {
      return storage
    }
  } catch {
    // Blocked storage is not fatal: the page-scoped stand-in below answers.
  }
  const existing = fallbacks.get(kind)
  if (existing) {
    return existing
  }
  const created = memoryStorage()
  fallbacks.set(kind, created)
  return created
}

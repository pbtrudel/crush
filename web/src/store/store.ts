import { useRef, useSyncExternalStore } from 'react'

export interface Store<T> {
  get(): T
  set(update: Partial<T> | ((state: T) => Partial<T>)): void
  subscribe(listener: () => void): () => void
}

export function createStore<T extends object>(initial: T): Store<T> {
  let state = initial
  const listeners = new Set<() => void>()
  return {
    get: () => state,
    set: (update) => {
      const patch = isUpdater(update) ? update(state) : update
      let changed = false
      for (const key of Object.keys(patch) as (keyof T)[]) {
        if (!Object.is(state[key], patch[key])) {
          changed = true
          break
        }
      }
      if (!changed) {
        return
      }
      state = { ...state, ...patch }
      for (const listener of listeners) {
        listener()
      }
    },
    subscribe: (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

// isUpdater narrows the union so the strict compiler sees the callable
// branch.
function isUpdater<T extends object>(update: Partial<T> | ((state: T) => Partial<T>)): update is (state: T) => Partial<T> {
  return typeof update === 'function'
}

// useStore reads a store through useSyncExternalStore. Selectors must return
// a primitive, or a value the caller has memoised: a fresh object on every
// read would loop.
export function useStore<T extends object, S>(store: Store<T>, selector: (state: T) => S): S {
  const cache = useRef<{ value: S } | null>(null)
  const getSnapshot = () => {
    const next = selector(store.get())
    if (cache.current === null || !Object.is(cache.current.value, next)) {
      cache.current = { value: next }
    }
    return cache.current.value
  }
  return useSyncExternalStore(store.subscribe, getSnapshot, getSnapshot)
}

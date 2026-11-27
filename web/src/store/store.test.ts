import { describe, expect, it, vi } from 'vitest'

import { createStore } from './store'

describe('createStore', () => {
  it('notifies subscribers on a partial update', () => {
    const store = createStore({ count: 0, label: 'a' })
    const listener = vi.fn()
    const stop = store.subscribe(listener)
    store.set({ count: 1 })
    expect(store.get()).toEqual({ count: 1, label: 'a' })
    expect(listener).toHaveBeenCalledTimes(1)
    stop()
    store.set({ count: 2 })
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('accepts an updater and publishes nothing for an equal value', () => {
    const store = createStore({ count: 0 })
    const listener = vi.fn()
    store.subscribe(listener)
    store.set(() => ({ count: 0 }))
    expect(listener).not.toHaveBeenCalled()
    store.set((state) => ({ count: state.count + 1 }))
    expect(listener).toHaveBeenCalledTimes(1)
  })
})

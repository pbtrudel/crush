import { describe, expect, it } from 'vitest'

import { memoryStorage, safeStorage } from './storage'

function block(storage: 'localStorage' | 'sessionStorage'): () => void {
  const original = Object.getOwnPropertyDescriptor(window, storage)
  Object.defineProperty(window, storage, {
    configurable: true,
    get(): Storage {
      throw new DOMException('blocked', 'SecurityError')
    },
  })
  return () => {
    if (original) {
      Object.defineProperty(window, storage, original)
    }
  }
}

describe('memoryStorage', () => {
  it('holds values for the life of the object', () => {
    const storage = memoryStorage({ 'crush.theme': 'dark' })
    expect(storage.getItem('crush.theme')).toBe('dark')
    storage.setItem('crush.token', 'abc123')
    expect(storage.getItem('crush.token')).toBe('abc123')
    expect(storage.length).toBe(2)
    storage.removeItem('crush.theme')
    expect(storage.key(0)).toBe('crush.token')
    storage.clear()
    expect(storage.length).toBe(0)
  })
})

describe('safeStorage', () => {
  it('answers the browser storage when it is there', () => {
    const original = Object.getOwnPropertyDescriptor(window, 'sessionStorage')
    const stub = memoryStorage()
    Object.defineProperty(window, 'sessionStorage', { configurable: true, value: stub })
    try {
      expect(safeStorage('session')).toBe(stub)
    } finally {
      if (original) {
        Object.defineProperty(window, 'sessionStorage', original)
      }
    }
  })

  it('falls back to an in-memory storage when the browser blocks the getter', () => {
    const restore = block('sessionStorage')
    try {
      const storage = safeStorage('session')
      storage.setItem('crush.token', 'abc123')
      expect(storage.getItem('crush.token')).toBe('abc123')
      // Every caller in the page shares one stand-in, so a token written by
      // the client is the token the next boot reads.
      expect(safeStorage('session')).toBe(storage)
    } finally {
      restore()
    }
  })

  it('keeps the two kinds apart', () => {
    const restoreLocal = block('localStorage')
    const restoreSession = block('sessionStorage')
    try {
      safeStorage('local').setItem('crush.theme', 'dark')
      expect(safeStorage('session').getItem('crush.theme')).toBeNull()
    } finally {
      restoreLocal()
      restoreSession()
    }
  })
})

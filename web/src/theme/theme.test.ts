import { describe, expect, it } from 'vitest'

import { memoryStorage } from '../app/storage'
import { applyTheme, readPreference, resolveTheme, writePreference } from './theme'

describe('resolveTheme', () => {
  it('follows the system when the preference says so', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
  })

  it('keeps an explicit preference whatever the system does', () => {
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })
})

describe('readPreference', () => {
  it('defaults to system', () => {
    expect(readPreference(memoryStorage())).toBe('system')
  })

  it('ignores a value it does not know', () => {
    expect(readPreference(memoryStorage({ 'crush.theme': 'neon' }))).toBe('system')
  })

  it('round-trips what writePreference wrote', () => {
    const storage = memoryStorage()
    writePreference('dark', storage)
    expect(readPreference(storage)).toBe('dark')
  })

  it('falls back to system when the browser blocks its storage', () => {
    const original = Object.getOwnPropertyDescriptor(window, 'localStorage')
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get(): Storage {
        throw new DOMException('blocked', 'SecurityError')
      },
    })
    try {
      // The page must paint even here: the theme builder asks for the
      // stored preference before React renders.
      expect(readPreference()).toBe('system')
    } finally {
      if (original) {
        Object.defineProperty(window, 'localStorage', original)
      }
    }
  })
})

describe('applyTheme', () => {
  it('writes the resolved theme on the document element', () => {
    const root = document.createElement('html')
    applyTheme(root, 'dark')
    expect(root.dataset.dsTheme).toBe('dark')
    applyTheme(root, 'light')
    expect(root.dataset.dsTheme).toBe('light')
  })
})

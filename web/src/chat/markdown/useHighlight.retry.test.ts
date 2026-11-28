import { describe, expect, it, vi } from 'vitest'

// This file's chunk import fails on its first evaluation and works after,
// which is what a retry looks like: it lives on its own so no earlier test
// in the same file has already loaded the mocked chunk.
const highlighter = vi.hoisted(() => ({ fail: true, tokenize: vi.fn() }))
vi.mock('./highlighter', () => {
  if (highlighter.fail) {
    throw new Error('chunk failed')
  }
  return { tokenize: highlighter.tokenize }
})

import { loadHighlighter } from './useHighlight'

describe('loadHighlighter', () => {
  // A chunk that failed to load must not poison every later block: the
  // cached promise is dropped, so the next block tries the import again.
  it('retries the chunk import after it fails', async () => {
    // Vitest wraps a mock factory's own error, so only the rejection is
    // asserted; the retry returning the module is the behaviour under test.
    await expect(loadHighlighter()).rejects.toThrow()
    highlighter.fail = false
    await expect(loadHighlighter()).resolves.toEqual({ tokenize: highlighter.tokenize })
  })
})

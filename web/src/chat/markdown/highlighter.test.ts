import { describe, expect, it, vi } from 'vitest'

// The core factory is mocked so a rejected core can be handed out once and
// a working one after it; the grammar is an empty registration because
// nothing here tokenizes for real.
const shiki = vi.hoisted(() => ({ createHighlighterCore: vi.fn() }))
vi.mock('shiki/core', () => ({
  createCssVariablesTheme: () => ({ name: 'css-variables', type: 'dark', colors: {}, tokenColors: [] }),
  createHighlighterCore: shiki.createHighlighterCore,
}))
vi.mock('shiki/engine/javascript', () => ({ createJavaScriptRegexEngine: () => ({}) }))
vi.mock('@shikijs/langs/json', () => ({ default: [] }))

import { tokenize } from './highlighter'

describe('highlighter', () => {
  // A core that failed to build must not poison every later block: the
  // cached promise is dropped, so the next block builds a core again.
  it('builds the core again after it rejects', async () => {
    shiki.createHighlighterCore
      .mockRejectedValueOnce(new Error('core failed'))
      .mockResolvedValue({ loadLanguage: async () => undefined, codeToTokensBase: () => [] })
    await expect(tokenize('{}', 'json')).rejects.toThrow('core failed')
    await expect(tokenize('{}', 'json')).resolves.toEqual([])
    expect(shiki.createHighlighterCore).toHaveBeenCalledTimes(2)
  })
})

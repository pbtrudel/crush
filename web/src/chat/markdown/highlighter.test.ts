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

  // A grammar that failed to load is not a grammar that cannot load: the
  // chunk may have been refused by a flaky connection. Keeping the rejected
  // promise in the loading map would leave every later block in that language
  // plain for the life of the tab.
  it('loads a grammar again after it rejects', async () => {
    vi.resetModules()
    const loadLanguage = vi
      .fn()
      .mockRejectedValueOnce(new Error('grammar failed'))
      .mockResolvedValue(undefined)
    shiki.createHighlighterCore.mockResolvedValue({ loadLanguage, codeToTokensBase: () => [] })
    const { tokenize: freshTokenize } = await import('./highlighter')
    await expect(freshTokenize('{}', 'json')).rejects.toThrow('grammar failed')
    await expect(freshTokenize('{}', 'json')).resolves.toEqual([])
    expect(loadLanguage).toHaveBeenCalledTimes(2)
  })
})

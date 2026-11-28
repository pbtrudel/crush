import { describe, expect, it } from 'vitest'

import { contrast, rgbOf, vars } from './contrast'
import design from './design-platform.css?raw'
import shiki from './shiki.css?raw'

const lightShiki = vars(shiki, 'light')
const darkShiki = vars(shiki, 'dark')
// The tokens shiki.css aliases live in the light blocks of the platform
// sheet, so one map resolves a --shiki-* value all the way to its rgb().
const lightSheet = new Map([...vars(design, 'light'), ...lightShiki])

describe('shiki.css', () => {
  // Shiki's css-variables theme spells a diff as token-inserted,
  // token-deleted and token-changed; without these the classes carry no
  // colour and a diff fence renders as plain text.
  it('defines a colour for inserted, deleted and changed, light and dark', () => {
    expect(lightShiki.get('shiki-token-inserted')).toBe(
      'color-mix(in srgb, var(--dsw-static-green-500) 45%, var(--dsw-static-green-900))',
    )
    expect(lightShiki.get('shiki-token-deleted')).toBe(
      'color-mix(in srgb, var(--dsw-static-red-600) 85%, var(--dsw-static-red-900))',
    )
    expect(lightShiki.get('shiki-token-changed')).toBe(
      'color-mix(in srgb, var(--dsw-static-amber-600) 60%, var(--dsw-static-amber-900))',
    )
    expect(darkShiki.get('shiki-token-inserted')).toBe('var(--dsw-alias-state-success-secondary)')
    expect(darkShiki.get('shiki-token-deleted')).toBe('var(--dsw-alias-state-error-secondary)')
    expect(darkShiki.get('shiki-token-changed')).toBe('var(--dsw-alias-state-warn-secondary)')
  })

  it('maps each diff class onto its variable', () => {
    for (const name of ['inserted', 'deleted', 'changed']) {
      expect(shiki).toContain(`.tok-${name} {`)
      expect(shiki).toContain(`color: var(--shiki-token-${name});`)
    }
  })

  // A light code block sits on --dsw-alias-markdown-code-block, the
  // bluish-50 surface. Every token colour must reach AA there, or the axe
  // scan flags every light-theme code block.
  it('keeps every light token colour at 4.5:1 against the code surface', () => {
    const surface = rgbOf('var(--dsw-alias-markdown-code-block)', lightSheet)
    const tokens = [...lightShiki].filter(([name]) => name.startsWith('shiki-token-'))
    expect(tokens).toHaveLength(12)
    const below = tokens
      .map(([name, value]) => ({ name, value, ratio: contrast(rgbOf(value, lightSheet), surface) }))
      .filter((token) => token.ratio < 4.5)
      .map((token) => `--${token.name}: ${token.value} at ${token.ratio.toFixed(2)}:1`)
    expect(below).toEqual([])
    expect(contrast(rgbOf('var(--shiki-foreground)', lightSheet), surface)).toBeGreaterThanOrEqual(4.5)
  })
})

import { describe, expect, it } from 'vitest'

import { contrast, rgbOf, vars } from './contrast'
import app from './app.css?raw'
import design from './design-platform.css?raw'

// app.css is imported after design-platform.css, so its light body rule is
// what the page resolves. The dark theme overrides the same token with a
// more specific selector, which the cascade below resolves.
const sheets = [
  { css: design, order: 0 },
  { css: app, order: 1 },
]
const light = new Map([...vars(design, 'light'), ...vars(app, 'light')])
const dark = new Map([...vars(design, 'dark'), ...vars(app, 'dark')])

interface Declaration {
  selector: string
  value: string
  order: number
}

// declarations reads every block that declares a custom property. Under a
// light html only the blocks without a dark selector match; under a dark
// html every block matches and specificity decides the winner.
function declarations(theme: 'light' | 'dark', property: string): Declaration[] {
  const out: Declaration[] = []
  for (const sheet of sheets) {
    for (const block of sheet.css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      if (theme === 'light' && block[1].includes('data-ds-theme')) {
        continue
      }
      const match = new RegExp(`--${property}:\\s*([^;]+);`).exec(block[2])
      if (match) {
        out.push({ selector: block[1].trim(), value: match[1].trim(), order: sheet.order })
      }
    }
  }
  return out
}

// specificity weighs the simple selectors the two theme sheets use: ids,
// classes, attributes and elements. Neither sheet uses pseudo-classes on
// the token blocks, and source order breaks a tie.
function specificity(selector: string): number {
  const ids = (selector.match(/#[\w-]+/g) ?? []).length
  const attributes = (selector.match(/\[[^\]]*\]/g) ?? []).length
  const classes = (selector.match(/\.[\w-]+/g) ?? []).length
  const elements = (selector.match(/(^|[\s>+~])([a-z][\w-]*)/g) ?? []).length
  return ids * 10_000 + (classes + attributes) * 100 + elements
}

// resolveVar picks the declaration a browser would apply to the body: the
// highest specificity wins, and a later import wins a tie.
function resolveVar(theme: 'light' | 'dark', property: string): Declaration {
  const candidates = declarations(theme, property)
  candidates.sort(
    (a, b) => specificity(b.selector) - specificity(a.selector) || b.order - a.order,
  )
  const winner = candidates[0]
  if (winner === undefined) {
    throw new Error(`no declaration of --${property} for ${theme}`)
  }
  return winner
}

describe('app.css', () => {
  // DSH's light tertiary label is 3.73:1 on the white surfaces the chat
  // draws its gutters, hunk headers, section labels and terminal prompt on,
  // below AA. The app moves it to the secondary step once for the whole
  // light theme; the dark theme keeps DSH's own value.
  it('keeps the light tertiary label at 4.5:1 on every surface it labels', () => {
    const value = resolveVar('light', 'dsw-alias-label-tertiary').value
    expect(value).toBe('var(--dsw-static-neutral-bluish-700)')
    const ink = rgbOf(value, light)
    // The surfaces behind a tertiary label in the chat: the page and the
    // tool cards sit on the base and the two layers, and a Markdown code
    // block and a diff deletion row are the tinted ones.
    const surfaces = [
      'var(--dsw-alias-bg-base)',
      'var(--dsw-alias-bg-layer-1)',
      'var(--dsw-alias-bg-layer-2)',
      'var(--dsw-alias-markdown-code-block)',
      'color-mix(in srgb, var(--dsw-static-red-400) 12%, var(--dsw-alias-bg-layer-2))',
    ]
    const below = surfaces
      .map((surface) => ({ surface, ratio: contrast(ink, rgbOf(surface, light)) }))
      .filter((entry) => entry.ratio < 4.5)
      .map((entry) => `${entry.surface} at ${entry.ratio.toFixed(2)}:1`)
    expect(below).toEqual([])
  })

  it("resolves the dark tertiary label over the light body rule", () => {
    const winner = resolveVar('dark', 'dsw-alias-label-tertiary')
    expect(winner.value).toBe('var(--dsw-static-neutral-bluish-400)')
    // The light body rule still applies under the dark html; only the dark
    // selector's higher specificity keeps DSH's value.
    const lightBody = declarations('light', 'dsw-alias-label-tertiary').find(
      (entry) => entry.selector === 'body',
    )
    expect(lightBody).toBeDefined()
    expect(specificity(winner.selector)).toBeGreaterThan(specificity(lightBody?.selector ?? ''))
    expect(contrast(rgbOf(winner.value, dark), rgbOf('var(--dsw-alias-bg-base)', dark))).toBeGreaterThanOrEqual(
      4.5,
    )
  })
})

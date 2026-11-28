import { describe, expect, it } from 'vitest'

import { contrast, rgbOf, vars } from './contrast'
import app from './app.css?raw'
import design from './design-platform.css?raw'

// app.css is imported after design-platform.css, so its light body rule is
// what the page resolves. The dark theme overrides the same token with a
// more specific selector, which vars() leaves out of the light map.
const light = new Map([...vars(design, 'light'), ...vars(app, 'light')])
const dark = new Map([...vars(design, 'dark'), ...vars(app, 'dark')])

describe('app.css', () => {
  // DSH's light tertiary label is 3.73:1 on the white surfaces the chat
  // draws its gutters, hunk headers, section labels and terminal prompt on,
  // below AA. The app moves it to the secondary step once for the whole
  // light theme; the dark theme keeps DSH's own value.
  it('keeps the light tertiary label at 4.5:1 on every surface it labels', () => {
    const value = light.get('dsw-alias-label-tertiary') ?? ''
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

  it("leaves the dark tertiary label at DSH's own step", () => {
    expect(dark.get('dsw-alias-label-tertiary')).toBe('var(--dsw-static-neutral-bluish-400)')
  })
})

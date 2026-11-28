import { describe, expect, it } from 'vitest'

import design from './design-platform.css?raw'
import shiki from './shiki.css?raw'

type Rgb = [number, number, number]

// vars parses the custom properties of every block in one theme, so an
// alias resolves across a token sheet: a block under data-ds-theme is the
// dark theme, every other block is the light one. Comments go first: a
// sheet's header names the dark selector, which would otherwise misread the
// light block that follows it.
function vars(css: string, theme: 'light' | 'dark'): Map<string, string> {
  const out = new Map<string, string>()
  for (const block of css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (block[1].includes('data-ds-theme') !== (theme === 'dark')) {
      continue
    }
    for (const declaration of block[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) {
      out.set(declaration[1], declaration[2].trim())
    }
  }
  return out
}

// rgbOf reads a colour the way a browser would: a hex or rgb() literal, an
// alias into the token sheets, or a color-mix() of two aliases, whose
// result is the share of the first colour plus the rest of the second,
// channel by channel, rounded to 8 bits.
function rgbOf(text: string, sheet: Map<string, string>): Rgb {
  const value = text.trim()
  const hex = /^#([0-9a-f]{6})$/i.exec(value)
  if (hex) {
    return [0, 2, 4].map((start) => Number.parseInt(hex[1].slice(start, start + 2), 16)) as Rgb
  }
  const rgb = /^rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$/.exec(value)
  if (rgb) {
    return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])]
  }
  const alias = /^var\(--([\w-]+)\)$/.exec(value)
  if (alias) {
    const target = sheet.get(alias[1])
    expect(target, `--${alias[1]}`).toBeDefined()
    return rgbOf(target ?? '', sheet)
  }
  const mix = /^color-mix\(in srgb,\s*var\(--([\w-]+)\)\s*(\d+)%,\s*var\(--([\w-]+)\)\s*\)$/.exec(value)
  if (mix) {
    const share = Number(mix[2]) / 100
    const first = rgbOf(`var(--${mix[1]})`, sheet)
    const second = rgbOf(`var(--${mix[3]})`, sheet)
    return first.map((channel, index) => Math.round(channel * share + second[index] * (1 - share))) as Rgb
  }
  throw new Error(`shiki.css: cannot read the colour ${value}`)
}

function channelLuminance(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

// luminance is WCAG's relative luminance; contrast is its ratio, which AA
// asks to be at least 4.5:1 for body text.
function luminance([red, green, blue]: Rgb): number {
  return 0.2126 * channelLuminance(red) + 0.7152 * channelLuminance(green) + 0.0722 * channelLuminance(blue)
}

function contrast(a: Rgb, b: Rgb): number {
  const [lighter, darker] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (lighter + 0.05) / (darker + 0.05)
}

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

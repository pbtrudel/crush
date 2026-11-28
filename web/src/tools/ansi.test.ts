import { describe, expect, it } from 'vitest'

import design from '../theme/design-platform.css?raw'
import terminal from './Terminal.module.css?raw'
import { parseAnsi } from './ansi'

describe('parseAnsi', () => {
  it('splits lines and keeps plain text unstyled', () => {
    expect(parseAnsi('one\ntwo')).toEqual([
      [{ text: 'one', classes: [] }],
      [{ text: 'two', classes: [] }],
    ])
  })

  it('maps colours and weights onto classes, and resets them', () => {
    expect(parseAnsi('\x1b[1;31mFAIL\x1b[0m ok')).toEqual([
      [
        { text: 'FAIL', classes: ['bold', 'fg-red'] },
        { text: ' ok', classes: [] },
      ],
    ])
    expect(parseAnsi('\x1b[92mgreen\x1b[39m')).toEqual([[{ text: 'green', classes: ['fg-bright-green'] }]])
  })

  it('drops extended colours with their parameters', () => {
    // Both runs keep the default colour, so they merge into one span, and
    // the 4 after 38;2;1;2;3 is never read as underline.
    expect(parseAnsi('\x1b[38;5;208morange\x1b[38;2;1;2;3;4m rgb')).toEqual([
      [
        { text: 'orange', classes: [] },
        { text: ' rgb', classes: ['underline'] },
      ],
    ])
    expect(parseAnsi('\x1b[38;5;208ma\x1b[38;2;1;2;3mb')).toEqual([[{ text: 'ab', classes: [] }]])
  })

  it('keeps the character after a two-byte escape', () => {
    expect(parseAnsi('\x1bMhello')).toEqual([[{ text: 'hello', classes: [] }]])
  })

  it('strips cursor moves, OSC titles and other escapes', () => {
    expect(parseAnsi('\x1b]0;title\x07a\x1b[2Kb\x1b[1Ac\x1b(B')).toEqual([[{ text: 'abc', classes: [] }]])
  })

  it('keeps the text after a carriage return, like a terminal', () => {
    expect(parseAnsi('10%\r50%\r100%\r\ndone')).toEqual([
      [{ text: '100%', classes: [] }],
      [{ text: 'done', classes: [] }],
    ])
  })

  it('keeps a progress line that ends with a carriage return', () => {
    expect(parseAnsi('50%\r')).toEqual([[{ text: '50%', classes: [] }]])
  })

  it('rewinds across escapes between two carriage returns', () => {
    expect(parseAnsi('10%\r\x1b[2K50%')).toEqual([[{ text: '50%', classes: [] }]])
  })

  it('renders an OSC 8 hyperlink as plain text', () => {
    expect(parseAnsi('\x1b]8;;https://example.com\x07link\x1b]8;;\x07')).toEqual([
      [{ text: 'link', classes: [] }],
    ])
  })

  it('drops control characters that no terminal shows', () => {
    expect(parseAnsi('a\x07b\x00c\x7f')).toEqual([[{ text: 'abc', classes: [] }]])
  })

  it('keeps a tab', () => {
    expect(parseAnsi('a\tb')).toEqual([[{ text: 'a\tb', classes: [] }]])
  })

  it('drops an escape cut off by the end of the text', () => {
    expect(parseAnsi('done\x1b[3')).toEqual([[{ text: 'done', classes: [] }]])
  })
})

type Rgb = [number, number, number]

// vars parses the custom properties of one theme, so a palette resolves
// across a token sheet: a block under a data-ds-theme selector is the dark
// theme, every other block is the light one. Comments go first: a header
// describes the dark selector, which would otherwise misread the light
// block that follows it.
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
  throw new Error(`Terminal.module.css: cannot read the colour ${value}`)
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

// declaration finds the last rule for one class and reads one property
// from it. CSS Modules keep a hyphenated class in the source unchanged,
// and the classes the parser emits are the ones checked here.
function declaration(property: 'color' | 'background', name: string): string | null {
  let found: string | null = null
  const source = terminal.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const block of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = block[1].split(',').map((selector) => selector.trim())
    if (!selectors.includes(`.${name}`)) {
      continue
    }
    const value = new RegExp(`${property}:\\s*([^;]+);`).exec(block[2])
    if (value) {
      found = value[1].trim()
    }
  }
  return found
}

const foreground = [
  'fg-black',
  'fg-white',
  'fg-bright-white',
  'fg-bright-black',
  'fg-red',
  'fg-bright-red',
  'fg-green',
  'fg-bright-green',
  'fg-yellow',
  'fg-bright-yellow',
  'fg-blue',
  'fg-bright-blue',
  'fg-magenta',
  'fg-bright-magenta',
  'fg-cyan',
  'fg-bright-cyan',
]

const background = foreground.map((name) => name.replace('fg-', 'bg-'))

const lightPalette = new Map([...vars(terminal, 'light')].filter(([name]) => name.startsWith('ansi-fg-')))
const darkPalette = new Map([...vars(terminal, 'dark')].filter(([name]) => name.startsWith('ansi-fg-')))
// The default colour is the theme's own label token, which resolves in
// both themes, so the dark block has no reason to repeat it.
const themeAware = new Set(['ansi-fg-default'])
// The tokens the palette aliases live in the platform sheet, so one map
// resolves a --ansi-fg-* value all the way to its rgb().
const lightSheet = new Map([...vars(design, 'light'), ...vars(terminal, 'light')])

describe('the ansi palette', () => {
  // A colour class with no rule leaves the span at the inherited colour,
  // which is legible but loses the colour the tool asked for.
  it('defines a colour for every class the parser emits', () => {
    expect(foreground.filter((name) => declaration('color', name) === null)).toEqual([])
    expect(background.filter((name) => declaration('background', name) === null)).toEqual([])
  })

  it('draws every colour class from a palette variable defined in both themes', () => {
    const unresolved = foreground
      .map((name) => ({ name, value: declaration('color', name) ?? '' }))
      .map(({ name, value }) => ({
        name,
        variable: /^var\(--([\w-]+)\)$/.exec(value)?.[1] ?? value,
      }))
      .filter(
        ({ variable }) =>
          !lightPalette.has(variable) || (!themeAware.has(variable) && !darkPalette.has(variable)),
      )
    expect(unresolved).toEqual([])
    // Guards against an empty palette passing the filter above.
    expect(lightPalette.size).toBeGreaterThanOrEqual(10)
    expect(darkPalette.size).toBeGreaterThanOrEqual(lightPalette.size - themeAware.size)
  })

  // The light card sits on --dsw-alias-bg-layer-2, the white surface.
  // Every colour a terminal can print must reach AA there, or a build log
  // is unreadable in the light theme.
  it('keeps every light colour at 4.5:1 against the terminal card', () => {
    // Guards against an empty palette passing the filter below.
    expect(lightPalette.size).toBeGreaterThanOrEqual(10)
    const surface = rgbOf('var(--dsw-alias-bg-layer-2)', lightSheet)
    const below = [...lightPalette]
      .map(([name, value]) => ({ name, ratio: contrast(rgbOf(value, lightSheet), surface) }))
      .filter((entry) => entry.ratio < 4.5)
      .map((entry) => `--${entry.name} at ${entry.ratio.toFixed(2)}:1`)
    expect(below).toEqual([])
  })
})

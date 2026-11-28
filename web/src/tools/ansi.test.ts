import { describe, expect, it } from 'vitest'

import { contrast, rgbOf, vars } from '../theme/contrast'
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

  it('keeps a progress line that ends in a return after its escapes', () => {
    expect(parseAnsi('\x1b[32m50%\x1b[0m\r')).toEqual([[{ text: '50%', classes: ['fg-green'] }]])
    expect(parseAnsi('50%\x1b[0m\r')).toEqual([[{ text: '50%', classes: [] }]])
  })

  it('clears the line for the next write, never for the return itself', () => {
    expect(parseAnsi('\x1b[2K10%\r\x1b[2K50%\x1b[K\r')).toEqual([[{ text: '50%', classes: [] }]])
  })

  it('keeps a line whose last write is a check mark', () => {
    expect(parseAnsi('\x1b[32m✓\x1b[0m done\r')).toEqual([
      [
        { text: '✓', classes: ['fg-green'] },
        { text: ' done', classes: [] },
      ],
    ])
    expect(parseAnsi('abc\x1b[1mX\r')).toEqual([
      [
        { text: 'abc', classes: [] },
        { text: 'X', classes: ['bold'] },
      ],
    ])
  })

  it('strips the escapes a terminal saves and restores state with', () => {
    expect(parseAnsi('\x1b850% done')).toEqual([[{ text: '50% done', classes: [] }]])
    expect(parseAnsi('\x1b=c\x1b>d\x1bce')).toEqual([[{ text: 'cde', classes: [] }]])
  })

  it('strips a CSI whatever its parameters and intermediates', () => {
    expect(parseAnsi('\x1b[>4;2mx')).toEqual([[{ text: 'x', classes: [] }]])
    expect(parseAnsi('\x1b[2 qy')).toEqual([[{ text: 'y', classes: [] }]])
    expect(parseAnsi('\x1b[!pz')).toEqual([[{ text: 'z', classes: [] }]])
    // Not an SGR: its parameters reset nothing.
    expect(parseAnsi('\x1b[31m\x1b[>4;2mx')).toEqual([[{ text: 'x', classes: ['fg-red'] }]])
  })

  it('strips a string sequence up to its terminator', () => {
    expect(parseAnsi('\x1bP1;2|text\x1b\\after')).toEqual([[{ text: 'after', classes: [] }]])
    expect(parseAnsi('\x1b_apc\x1b\\after')).toEqual([[{ text: 'after', classes: [] }]])
    expect(parseAnsi('\x1b^pm\x1b\\after')).toEqual([[{ text: 'after', classes: [] }]])
    expect(parseAnsi('\x1bXsos\x1b\\after')).toEqual([[{ text: 'after', classes: [] }]])
  })

  it('costs one line for a string sequence that is never terminated', () => {
    expect(parseAnsi('a\x1bPq\nb\nc\n')).toEqual([
      [{ text: 'a', classes: [] }],
      [{ text: 'b', classes: [] }],
      [{ text: 'c', classes: [] }],
      [],
    ])
    expect(parseAnsi('x\x1bP\u009cy')).toEqual([[{ text: 'xy', classes: [] }]])
  })

  it('drops a CSI a line break cut in half', () => {
    expect(parseAnsi('\x1b[3\nnext')).toEqual([[], [{ text: 'next', classes: [] }]])
  })

  it('reads an SGR only from a plain parameter list', () => {
    expect(parseAnsi('\x1b[31m\x1b[0 mx')).toEqual([[{ text: 'x', classes: ['fg-red'] }]])
  })

  it('drops an 8-bit CSI with its parameters', () => {
    expect(parseAnsi('a\u009b31mb')).toEqual([[{ text: 'ab', classes: [] }]])
  })

  it('costs one line for a title that is never terminated', () => {
    expect(parseAnsi('\x1b]0;title\nnext')).toEqual([[], [{ text: 'next', classes: [] }]])
    expect(parseAnsi('\x1b]0;title\u009cnext')).toEqual([[{ text: 'next', classes: [] }]])
  })

  it('drops the C1 controls a terminal takes for bytes', () => {
    // The single-byte CSI has a case of its own: it swallows its final
    // byte, so only the controls that are not a sequence are left here.
    expect(parseAnsi('a\u0085b\u009cc')).toEqual([[{ text: 'abc', classes: [] }]])
  })

  it('reads a colon sub-parameter group as one code', () => {
    expect(parseAnsi('\x1b[38:2::1:2:3mx')).toEqual([[{ text: 'x', classes: [] }]])
    expect(parseAnsi('\x1b[4:3mx')).toEqual([[{ text: 'x', classes: ['underline'] }]])
    expect(parseAnsi('\x1b[4:0mx')).toEqual([[{ text: 'x', classes: [] }]])
  })
})

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

  // Faint text is a colour, not an opacity: opacity blends the text into
  // whatever is behind it and drops a passing colour under AA.
  it('draws dim text in a colour that still reaches 4.5:1', () => {
    const surface = rgbOf('var(--dsw-alias-bg-layer-2)', lightSheet)
    const value = declaration('color', 'dim') ?? ''
    const name = /^var\(--([\w-]+)\)$/.exec(value)?.[1] ?? value
    expect(lightPalette.has(name)).toBe(true)
    expect(contrast(rgbOf(value, lightSheet), surface)).toBeGreaterThanOrEqual(4.5)
    // Comments are not rules: only a declaration may hide the colour.
    expect(/\bopacity\s*:/.test(terminal.replace(/\/\*[\s\S]*?\*\//g, ''))).toBe(false)
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

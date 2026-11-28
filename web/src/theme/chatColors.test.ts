import { describe, expect, it } from 'vitest'

import copy from '../chat/CopyButton.module.css?raw'
import markdown from '../chat/markdown/Markdown.module.css?raw'
import message from '../chat/MessageView.module.css?raw'
import caps from '../tools/Tools.module.css?raw'
import { contrast, rgbOf, vars, type Rgb } from './contrast'
import app from './app.css?raw'
import design from './design-platform.css?raw'

// The theme sheets resolve the aliases; app.css is the shell's layer on
// top of DSH's tokens.
const light = new Map([...vars(design, 'light'), ...vars(app, 'light')])
const dark = new Map([...vars(design, 'dark'), ...vars(app, 'dark')])

const lightCaps = vars(caps, 'light')
const darkCaps = vars(caps, 'dark')
const lightCopy = vars(copy, 'light')
const lightMessage = vars(message, 'light')
const lightMarkdown = vars(markdown, 'light')
const darkMarkdown = vars(markdown, 'dark')

// blend paints a foreground at one opacity over an opaque surface, which is
// what a faded action looks like after the browser composites it.
function blend(ink: Rgb, surface: Rgb, opacity: number): Rgb {
  return ink.map((channel, index) =>
    Math.round(channel * opacity + surface[index] * (1 - opacity)),
  ) as Rgb
}

describe('chat colours', () => {
  // The light state tokens are too pale for 11 px capsule text on the white
  // card. Each light value is a mix with its darkest neighbour and must
  // reach AA; the error capsule also sits on the red wash its background
  // paints.
  it('keeps every light status capsule at 4.5:1 on its surface', () => {
    const surfaces = {
      layer2: 'var(--dsw-alias-bg-layer-2)',
      errorWash: 'color-mix(in srgb, var(--dsw-static-red-400) 12%, var(--dsw-alias-bg-layer-2))',
    } as const
    const cases = [
      ['capsule-running-fg', 'layer2'],
      ['capsule-done-fg', 'layer2'],
      ['capsule-interrupted-fg', 'layer2'],
      ['capsule-error-fg', 'layer2'],
      ['capsule-error-fg', 'errorWash'],
    ] as const
    const below = cases
      .map(([name, surface]) => {
        const ink = rgbOf(lightCaps.get(name) ?? '', light)
        return {
          name,
          surface,
          ratio: contrast(ink, rgbOf(surfaces[surface], light)),
        }
      })
      .filter((entry) => entry.ratio < 4.5)
    expect(below).toEqual([])
  })

  it('keeps the dark status capsules on their theme tokens', () => {
    expect(darkCaps.get('capsule-running-fg')).toBe('var(--dsw-alias-state-business-primary)')
    expect(darkCaps.get('capsule-done-fg')).toBe('var(--dsw-alias-state-success-primary)')
    expect(darkCaps.get('capsule-interrupted-fg')).toBe('var(--dsw-alias-state-warn-primary)')
    expect(darkCaps.get('capsule-error-fg')).toBe('var(--dsw-alias-state-error-primary)')
  })

  // The copy action fades to 60% until the message is hovered. The label
  // stays the primary colour so the faded text still reaches AA; the fade
  // comes from MessageView.module.css, so a new opacity is measured too.
  it('keeps the faded Copy label at 4.5:1 on the light surface', () => {
    const surface = rgbOf('var(--dsw-alias-bg-layer-2)', light)
    const ink = rgbOf(lightCopy.get('copy-label') ?? '', light)
    const fade = Number(lightMessage.get('copy-fade'))
    expect(fade).toBeGreaterThan(0)
    expect(fade).toBeLessThan(1)
    expect(contrast(blend(ink, surface, fade), surface)).toBeGreaterThanOrEqual(4.5)
    // The dark surface uses the same blend; check it too.
    const darkSurface = rgbOf('var(--dsw-alias-bg-layer-2)', dark)
    const darkInk = rgbOf('var(--dsw-alias-label-primary)', dark)
    expect(contrast(blend(darkInk, darkSurface, fade), darkSurface)).toBeGreaterThanOrEqual(4.5)
  })

  // The light business token is 4.23:1 on the page; the darker mix reaches
  // 5.55:1. The dark theme keeps the token, which is legible on its
  // surfaces.
  it('keeps the light Markdown link at 4.5:1 and the dark one on the token', () => {
    const value = lightMarkdown.get('markdown-link') ?? ''
    expect(value).toBe(
      'color-mix(in srgb, var(--dsw-static-deepseek-500) 75%, var(--dsw-static-deepseek-900))',
    )
    const ink = rgbOf(value, light)
    for (const surface of [
      'var(--dsw-alias-bg-base)',
      'var(--dsw-alias-bg-layer-1)',
      'var(--dsw-alias-bg-layer-2)',
    ]) {
      expect(contrast(ink, rgbOf(surface, light))).toBeGreaterThanOrEqual(4.5)
    }
    expect(darkMarkdown.get('markdown-link')).toBe('var(--dsw-alias-state-business-primary)')
  })
})

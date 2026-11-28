// contrast.ts is a test helper. It reads the colours a theme sheet
// declares and measures their WCAG contrast, so the stylesheet tests can
// pin a palette to AA instead of trusting a screenshot. Only tests import
// it; nothing in the app bundle touches it.
export type Rgb = [number, number, number]

// vars parses the custom properties of one theme, so an alias resolves
// across a token sheet: a block under a data-ds-theme selector is the dark
// theme, every other block is the light one. Comments go first: a header
// names the dark selector, which would otherwise misread the light block
// that follows it.
export function vars(css: string, theme: 'light' | 'dark'): Map<string, string> {
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
export function rgbOf(text: string, sheet: Map<string, string>): Rgb {
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
    if (target === undefined) {
      throw new Error(`the sheet does not define --${alias[1]}`)
    }
    return rgbOf(target, sheet)
  }
  const mix = /^color-mix\(in srgb,\s*var\(--([\w-]+)\)\s*(\d+)%,\s*var\(--([\w-]+)\)\s*\)$/.exec(value)
  if (mix) {
    const share = Number(mix[2]) / 100
    const first = rgbOf(`var(--${mix[1]})`, sheet)
    const second = rgbOf(`var(--${mix[3]})`, sheet)
    return first.map((channel, index) => Math.round(channel * share + second[index] * (1 - share))) as Rgb
  }
  throw new Error(`the sheet uses a colour this helper cannot read: ${value}`)
}

function channelLuminance(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

// luminance is WCAG's relative luminance; contrast is its ratio, which AA
// asks to be at least 4.5:1 for body text.
export function luminance([red, green, blue]: Rgb): number {
  return 0.2126 * channelLuminance(red) + 0.7152 * channelLuminance(green) + 0.0722 * channelLuminance(blue)
}

export function contrast(a: Rgb, b: Rgb): number {
  const [lighter, darker] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (lighter + 0.05) / (darker + 0.05)
}

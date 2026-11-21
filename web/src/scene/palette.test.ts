import { describe, expect, it } from 'vitest';
import { parseOklch, token } from './palette';

// close compares two 0..1 sRGB channels; the tolerance absorbs rounding
// differences between conversion implementations.
const close = (a: number, b: number) => Math.abs(a - b) < 0.01;

// channels decodes a sRGB hex string from three's getHexString.
function channels(hex: string): number[] {
  return [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
}

describe('palette', () => {
  it('parses oklch tokens', () => {
    const c = parseOklch('oklch(0.82 0.14 210)')!;
    expect(c.b).toBeGreaterThan(c.r);
    expect(c.g).toBeGreaterThan(c.r);
    expect(parseOklch('#52d8ff')).toBeNull();
  });

  it('converts a known oklch value to its sRGB color', () => {
    // oklch(0.82 0.14 210) is the --cyan token. The expected sRGB comes from
    // the OKLab to sRGB matrices, not from this implementation.
    const rgb = channels(parseOklch('oklch(0.82 0.14 210)')!.getHexString());
    expect(close(rgb[0], 0.0745)).toBe(true);
    expect(close(rgb[1], 0.8627)).toBe(true);
    expect(close(rgb[2], 0.9647)).toBe(true);
    expect(parseOklch('oklch(1 0 0)')!.getHexString()).toBe('ffffff');
  });

  it('falls back when the token is missing, as in jsdom', () => {
    expect(token('--missing', '#52d8ff').getHexString()).toBe('52d8ff');
  });

  it('reads and converts a color token set on the document', () => {
    document.documentElement.style.setProperty('--test-cyan', 'oklch(0.82 0.14 210)');
    try {
      const rgb = channels(token('--test-cyan', '#000000').getHexString());
      expect(close(rgb[0], 0.0745)).toBe(true);
      expect(close(rgb[1], 0.8627)).toBe(true);
      expect(close(rgb[2], 0.9647)).toBe(true);
    } finally {
      document.documentElement.style.removeProperty('--test-cyan');
    }
  });
});

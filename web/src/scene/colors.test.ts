import { describe, expect, it } from 'vitest';
import { readColors } from './colors';

describe('readColors', () => {
  it('falls back to the sRGB value of each token', () => {
    const c = readColors();
    expect(c.void.getHexString()).toBe('030318');
    expect(c.haze.getHexString()).toBe('3d006f');
    expect(c.cyan.getHexString()).toBe('00efff');
    expect(c.magenta.getHexString()).toBe('ff24d5');
    expect(c.violet.getHexString()).toBe('9754ff');
    expect(c.teal.getHexString()).toBe('15e5b4');
    expect(c.amber.getHexString()).toBe('ffb51c');
  });
});

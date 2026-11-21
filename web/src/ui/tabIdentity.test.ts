import { describe, expect, it } from 'vitest';
import { faviconFor, projectHue, tabTitle } from './tabIdentity';

describe('tab identity', () => {
  it('hue is stable per project and in range', () => {
    expect(projectHue('/repo/a')).toBe(projectHue('/repo/a'));
    expect(projectHue('/repo/a')).not.toBe(projectHue('/repo/b'));
    expect(projectHue('/x')).toBeGreaterThanOrEqual(0);
    expect(projectHue('/x')).toBeLessThan(360);
  });
  it('favicon is an svg data url', () => {
    expect(faviconFor(200)).toMatch(/^data:image\/svg\+xml,/);
  });
  it('title flags attention', () => {
    expect(tabTitle('crush', 'Parser refactor', false)).toBe('crush · Parser refactor');
    expect(tabTitle('crush', '', true)).toBe('(!) crush · New session');
  });
});

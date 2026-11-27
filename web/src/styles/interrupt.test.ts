import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as tokens.test.ts does.
const file = './interrupt.css';
const css = readFileSync(new URL(file, import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .replace(/\s+/g, ' ');

// rule returns the body of the first rule with the exact selector.
const rule = (selector: string) =>
  new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, '\\$&')} \\{([^}]*)\\}`).exec(css)?.[1] ?? '';

describe('interrupt dialog scroll region', () => {
  it('leaves the dialog frame unclipped so the danger glow can paint outside it', () => {
    // The ::before pulse lives on .interrupt; an overflow on the frame would
    // clip the 64px glow to the padding box (spec 3.9).
    const dialog = rule('.interrupt');
    expect(dialog).toContain('overflow: visible');
    expect(dialog).not.toMatch(/overflow-y|max-block-size/);
  });

  it('scrolls the dialog content in an inner layer with the same gutter', () => {
    const scroll = rule('.interrupt__scroll');
    expect(scroll).toContain('overflow-y: auto');
    expect(scroll).toContain('max-block-size: calc(100dvh - 64px - 2px)');
    expect(scroll).toContain('padding: var(--space-9)');
    expect(scroll).toContain('display: grid');
    expect(scroll).toContain('gap: 12px');
  });

  it('pulses the danger glow on the frame instead of the scroller', () => {
    expect(css).toMatch(
      /\.interrupt\[data-danger='high'\]::before \{[^}]*box-shadow: 0 0 0 1px var\(--magenta\), 0 0 64px/,
    );
    expect(css).not.toMatch(/\.interrupt__scroll[^{]*\{[^}]*animation/);
  });

  it('keeps the params block scrolling on its own', () => {
    const params = rule('.interrupt__params');
    expect(params).toContain('overflow: auto');
    expect(params).toContain('max-block-size: 30vh');
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as tokens.test.ts does.
const file = './inspector.css';
const css = readFileSync(new URL(file, import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .replace(/\s+/g, ' ');

// rule returns the body of the first rule with the exact selector.
const rule = (selector: string) =>
  new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, '\\$&')} \\{([^}]*)\\}`).exec(css)?.[1] ?? '';

describe('the inspector message box', () => {
  it('takes the glass surface instead of the user agent field', () => {
    // An unstyled textarea paints the browser's field colour, which read as
    // a pale olive block in the middle of the glass panel.
    const box = rule('.inspector__message textarea');
    expect(box).toContain('background: var(--void-deep)');
    expect(box).toContain('color: var(--text)');
    expect(box).toContain('border: 1px solid var(--edge)');
    // The panel is a fixed grid cell, so the box does not resize.
    expect(box).toContain('resize: none');
  });
});

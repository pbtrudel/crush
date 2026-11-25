import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as tokens.test.ts does.
const read = (name: string) =>
  readFileSync(new URL('./' + name, import.meta.url), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/\s+/g, ' ');

// rule returns the body of the first rule with the exact selector.
const rule = (css: string, selector: string) =>
  new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, '\\$&')} \\{([^}]*)\\}`).exec(css)?.[1] ?? '';

const signin = read('signin.css');

describe('the AWS SSO card', () => {
  it('sits above the whole deck column and never grows past the HUD', () => {
    const card = rule(signin, '.aws-sso');
    // --deck-h now measures the side answer plus the deck, and --hud-h keeps
    // the top strip clear when the command or the error text wraps.
    expect(card).toContain('inset-block-end: calc(var(--space-4) * 2 + var(--deck-h, 7.5rem))');
    expect(card).toContain('max-block-size: calc(100dvh - var(--hud-h, 96px) - var(--deck-h, 7.5rem) - var(--space-4) * 4)');
    expect(card).toContain('overflow-y: auto');
  });
});

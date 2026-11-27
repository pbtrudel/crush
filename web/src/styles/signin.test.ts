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

// atRule returns the body of the first at-rule that starts with the query.
const atRule = (css: string, query: string) => {
  const start = css.indexOf(query);
  if (start < 0) return '';
  const open = css.indexOf('{', start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) return css.slice(open + 1, i);
  }
  return '';
};

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

  it('anchors the phone card to the deck top the shell publishes', () => {
    // A short shell lets the deck run past the viewport, so viewport minus
    // --deck-h is not the deck's top; --deck-top is the measured edge.
    const phone = rule(atRule(signin, '@media (max-width: 640px)'), '.aws-sso');
    const top = 'var(--deck-top, calc(100dvh - var(--deck-h, 13rem) - var(--space-4)))';
    expect(phone).toContain(`inset-block-end: calc(100dvh - ${top} + var(--space-1))`);
    expect(phone).toContain(`max-block-size: calc(${top} - var(--hud-h, 184px) - var(--space-4) - var(--space-1) * 2)`);
  });

  it('packs the card into the short strip of a short phone', () => {
    const short = atRule(signin, '@media (max-width: 640px) and (max-height: 700px)');
    const card = rule(short, '.aws-sso');
    expect(card).toContain('display: flex');
    expect(card).toContain('flex-wrap: wrap');
    expect(card).toContain('padding: 6px 10px');
    expect(rule(short, '.aws-sso__title')).toContain('font-size: 0.75rem');
    expect(rule(short, '.aws-sso__command')).toContain('font-size: 0.8125rem');
    expect(rule(short, '.aws-sso__url, .aws-sso__status')).toContain('font-size: 0.75rem');
  });
});

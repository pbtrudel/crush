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

const shell = read('shell.css');
const hud = read('hud.css');

describe('HUD server groups', () => {
  it('separates the LSP group from the MCP one', () => {
    expect(rule(hud, '.hud__servers')).toContain('align-items: center');
    expect(hud).toMatch(
      /\.hud__servers \.hexes \+ \.hud-label \{[^}]*border-inline-start: 1px solid var\(--edge\)/,
    );
    // The label text keeps a readable gap from the divider.
    expect(hud).toMatch(/\.hud__servers \.hexes \+ \.hud-label \{[^}]*padding-inline-start: 8px/);
  });

  it('keeps the group button unchanged at rest', () => {
    const button = rule(hud, '.hud__servers-btn');
    expect(button).toContain('margin: 0');
    expect(button).toContain('padding: 0');
    expect(button).toContain('border: 0');
    expect(button).toContain('background: none');
    expect(button).toContain('font: inherit');
    expect(hud).toMatch(
      /\.hud__servers-btn:hover,\s*\.hud__servers-btn:focus-visible \{[^}]*color: var\(--cyan\)/,
    );
  });

  it('lights only the ready tiles when the group is hovered', () => {
    // The hover fill belongs to the lit tiles alone, so a stopped or failed
    // server never reads as ready while the pointer crosses the group.
    const lit = rule(
      hud,
      ".hud__servers-btn:hover .hex[data-tone='lit'] polygon, "
        + ".hud__servers-btn:focus-visible .hex[data-tone='lit'] polygon",
    );
    expect(lit).toContain('fill: var(--cyan)');
    expect(hud).not.toContain('.hud__servers-btn:hover .hex polygon');
    expect(hud).not.toContain('.hud__servers-btn:focus-visible .hex polygon');

    // The outline and the text glow still cover the whole button.
    const glow = rule(hud, '.hud__servers-btn:hover, .hud__servers-btn:focus-visible');
    expect(glow).toContain('outline: 1px solid var(--cyan)');
    expect(glow).toContain('text-shadow: var(--glow-text)');
  });
});

describe('HUD instrument row', () => {
  it('tightens the seven columns instead of wrapping from 1280px up', () => {
    // The wrapped layout must start below 1280, where the columns no longer
    // fit on one row.
    expect(atRule(shell, '@media (max-width: 1439px)')).not.toContain('flex-wrap: wrap');
    const wrapped = atRule(shell, '@media (max-width: 1279px)');
    expect(rule(wrapped, '.hud')).toContain('flex-wrap: wrap');

    // Between 1280 and 1439 the grid keeps its columns and gives up gutter
    // and gap instead.
    const tight = atRule(shell, '@media (max-width: 1439px)');
    expect(rule(tight, '.hud')).toContain('gap: var(--space-1) 10px');
    expect(rule(tight, '.hud')).toContain('padding: var(--space-4) 12px');
    expect(rule(tight, '.hud__controls')).toContain('gap: var(--space-1)');

    // The wrapped layout keeps the wide gutter it had before the tightening.
    expect(rule(wrapped, '.hud')).toContain('padding: var(--space-4) calc(var(--space-4) * 1.5)');
    expect(rule(wrapped, '.hud__controls')).toContain('gap: var(--space-4)');
  });

  it('keeps the taller toast fallback with the wrapped HUD', () => {
    expect(atRule(shell, '@media (max-width: 1279px)')).toContain('--hud-h, 184px');
    expect(atRule(shell, '@media (max-width: 1023px)')).not.toContain('--hud-h');
    expect(atRule(shell, '@media (max-width: 1023px)')).toContain('.holodeck__grid');
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as agents.test.ts does.
const file = './log.css';
const css = readFileSync(new URL(file, import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .replace(/\s+/g, ' ');

// rule returns the body of the first rule with the exact selector. Every
// regex metacharacter is escaped, because the cell selector uses :where().
const rule = (selector: string) =>
  new RegExp(`(?:^|\\})\\s*${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} \\{([^}]*)\\}`).exec(css)?.[1] ?? '';

describe('log markdown tables', () => {
  it('scrolls a wide table inside the message', () => {
    // A table wider than the log scrolls sideways in its own box instead
    // of pushing the panel or clipping its columns.
    const table = rule('.md table');
    expect(table).toContain('display: block');
    expect(table).toContain('overflow-x: auto');
  });

  it('sizes a column by its longest word, not one character', () => {
    // The transcript wraps prose anywhere; a cell that inherited that
    // would let auto table layout squeeze a column to one character.
    const cells = rule('.md :where(th, td)');
    expect(cells).toContain('overflow-wrap: normal');
    expect(cells).toContain('word-break: normal');
  });

  it('still wraps prose anywhere', () => {
    // The table fix must not give up the overflow guard that long prose
    // and URLs rely on.
    expect(rule('.md')).toContain('overflow-wrap: anywhere');
  });
});

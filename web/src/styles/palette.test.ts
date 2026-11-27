import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The M1 palette before the neon redesign. Stylesheets reach colors
// through the tokens (or color-mix over them), never through these.
const RETIRED = ['0.82 0.14 210', '0.68 0.24 330', '0.60 0.20 290', '0.80 0.15 75'];
const DIR = '.';

// retired matches one value however its numbers are spaced, so a retired
// color rewritten with a different separator still trips the scan.
function retired(value: string): RegExp {
  const parts = value.split(' ').map((part) => part.replace('.', '\\.'));
  return new RegExp(parts.join('\\s+'));
}

describe('palette', () => {
  it('keeps retired colors out of every stylesheet', () => {
    const dir = fileURLToPath(new URL(DIR, import.meta.url));
    const sheets = readdirSync(dir).filter((f) => f.endsWith('.css'));
    // A scan of an empty directory would pass without reading a sheet.
    expect(sheets.length).toBeGreaterThan(5);
    for (const sheet of sheets) {
      const css = readFileSync(path.join(dir, sheet), 'utf8');
      for (const value of RETIRED) expect(css, `${sheet} keeps ${value}`).not.toMatch(retired(value));
    }
  });
});

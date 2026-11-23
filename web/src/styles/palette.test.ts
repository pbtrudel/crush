import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The M1 palette before the neon redesign. Stylesheets reach colors
// through the tokens (or color-mix over them), never through these.
const RETIRED = ['0.82 0.14 210', '0.68 0.24 330', '0.60 0.20 290', '0.80 0.15 75'];
const DIR = '.';

describe('palette', () => {
  it('keeps retired colors out of every stylesheet', () => {
    const dir = fileURLToPath(new URL(DIR, import.meta.url));
    for (const sheet of readdirSync(dir).filter((f) => f.endsWith('.css'))) {
      const css = readFileSync(path.join(dir, sheet), 'utf8');
      for (const value of RETIRED) expect(css, `${sheet} keeps ${value}`).not.toContain(value);
    }
  });
});

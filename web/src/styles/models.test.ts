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

const models = read('models.css');

describe('the model selector busy state', () => {
  it('dims the reasoning content, never the control the focus is on', () => {
    // The fieldset holds the radio or the switch, so an opacity on it would
    // dim the focus ring while the answer is on its way.
    expect(rule(models, '.models__reasoning--busy')).toBe('');
    for (const content of ['models__legend', 'models__note', 'models__radio-text', 'models__switch-text']) {
      expect(models).toMatch(new RegExp(`\\.models__reasoning--busy \\.${content}[^}]*opacity: 0\\.6`));
    }
  });

  it('marks the current row with the data attribute, not the selected state', () => {
    expect(rule(models, ".models__option[data-current='true']")).toContain('border-inline-start-color: var(--cyan)');
    expect(rule(models, ".models__option[aria-selected='true']")).toBe('');
  });
});

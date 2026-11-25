import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as tokens.test.ts does.
const read = (name: string) =>
  readFileSync(new URL('./' + name, import.meta.url), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/\s+/g, ' ');

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

const team = read('team.css');

describe('the team panels at 1024px', () => {
  it('stacks the lanes and hides the todo ring below the desktop width', () => {
    const narrow = atRule(team, '@media (max-width: 1023px)');
    expect(narrow).toContain('.taskboard__lanes { grid-template-columns: 1fr; }');
    expect(narrow).toContain('.todo-orbit { display: none; }');
  });

  it('keeps three lanes and the ring at the desktop width and above', () => {
    // The three-column lane grid is the base rule, so the breakpoint is what
    // removes it rather than what adds it.
    expect(team).toMatch(/\.taskboard__lanes \{[^}]*grid-template-columns: repeat\(3/);
    expect(atRule(team, '@media (max-width: 1023px)')).not.toContain('.taskboard__lanes { grid-template-columns: repeat(');
  });
});

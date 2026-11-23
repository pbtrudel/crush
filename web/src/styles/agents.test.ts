import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file names are passed through variables, as tokens.test.ts does.
const file = './agents.css';
const css = readFileSync(new URL(file, import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .replace(/\s+/g, ' ');

// rule returns the body of the first rule with the exact selector.
const rule = (selector: string, source = css) =>
  new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, '\\$&')} \\{([^}]*)\\}`).exec(source)?.[1] ?? '';

const zIndex = (body: string) => Number(/z-index: (\d+)/.exec(body)?.[1]);

// Vite rewrites a literal path in new URL(), so the HUD sheet is read through
// a variable too.
const shellFile = './shell.css';
const shell = readFileSync(new URL(shellFile, import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .replace(/\s+/g, ' ');

describe('agent tags', () => {
  it('takes no clicks through a hidden tag, and hides it by opacity alone', () => {
    const hidden = rule(".agent-tag[data-visible='false']");
    expect(hidden).toContain('pointer-events: none');
    expect(hidden).toContain('opacity: 0');
    // A removed box would still leave the tag in the layout; only the paint
    // may go, so the transform keeps its place for the next frame.
    expect(hidden).not.toMatch(/visibility|display/);
  });

  it('lets the visible tags take clicks while their layer takes none', () => {
    expect(rule('.agent-tags')).toContain('pointer-events: none');
    expect(rule('.agent-tag')).toContain('pointer-events: auto');
  });

  it('stacks the agent list above the tags and both below the HUD', () => {
    const tags = zIndex(rule('.agent-tags'));
    const list = zIndex(rule('.agent-list'));
    const hud = zIndex(rule('.hud', shell));
    expect(tags).toBeGreaterThan(0);
    // A tag may sit over the list; the list is the accessible path and must
    // still win the click there.
    expect(list).toBeGreaterThan(tags);
    // The HUD keeps painting over both.
    expect(hud).toBeGreaterThan(list);
  });
});

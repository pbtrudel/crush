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
    // The user handle stays off; the box grows with its text instead.
    expect(box).toContain('resize: none');
    expect(box).toContain('field-sizing: content');
    expect(box).toContain('max-block-size: 8rem');
  });
});

// The panel is a stack of head, facts, actions and transcript inside a fixed
// grid cell, so every row the chrome gives up is a row the transcript keeps.
describe('the inspector stack', () => {
  it('puts Send beside the message box, not under it', () => {
    const form = rule('.inspector__message');
    expect(form).toContain('grid-template-columns: minmax(0, 1fr) auto');
    expect(form).toContain('align-items: end');
    expect(rule(".inspector__message [data-action='message']")).toContain('grid-row: 2');
  });

  it('reads each fact as one label and value pair', () => {
    expect(rule('.inspector__facts')).toContain('display: flex');
    expect(rule('.inspector__facts > div')).toContain('display: flex');
  });

  it('gives an empty result line no room but keeps it in the tree', () => {
    // The line is a live region: display: none would take it out of the
    // accessibility tree, so the region could never announce anything.
    const notice = rule('.inspector__notice:empty');
    expect(notice).toContain('block-size: 0');
    expect(notice).toContain('overflow: hidden');
    expect(notice).not.toContain('display: none');
  });

  it('lets the transcript keep the height the chrome leaves', () => {
    expect(rule('.panel--inspector .log')).toContain('flex: 1');
    expect(rule('.panel--inspector .log')).toContain('padding-block: 8px');
  });
});

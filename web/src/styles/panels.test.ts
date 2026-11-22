import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Vite rewrites a literal path in new URL() into a served asset URL, so the
// file name is passed through a variable, as tokens.test.ts does.
const file = './panels.css';
const css = readFileSync(new URL(file, import.meta.url), 'utf8').replace(/\s+/g, ' ');

describe('panel primitives', () => {
  it('blurs glass and falls back to the solid token without backdrop-filter', () => {
    expect(css).toContain('.glass {');
    expect(css).toContain('backdrop-filter: blur(var(--blur-glass));');
    expect(css).toContain('@supports not (backdrop-filter: blur(1px))');
    expect(css).toContain('background: var(--glass-solid);');
  });

  it('gives alert glass magenta edges and brackets', () => {
    expect(css).toContain('.glass--alert {');
    expect(css).toMatch(/\.glass--alert \{[^}]*--bracket: var\(--magenta\)/);
  });

  it('draws holograms without a fill and flickers them once', () => {
    expect(css).toMatch(/\.holo \{[^}]*background: oklch\(0\.12 0\.05 275 \/ 0\.2\)/);
    expect(css).toContain('@keyframes flicker-once');
    expect(css).toContain('@keyframes power-up');
  });

  it('turns off flicker and glitch for reduced motion', () => {
    expect(css).toMatch(/\[data-motion='reduced'\] \.flicker-once[^{]*\{[^}]*animation-name: fade-in/);
  });

  it('edges glass with the cyan glow token and isolates scan lines', () => {
    expect(css).toMatch(/\.glass \{[^}]*box-shadow: var\(--glow-cyan\)/);
    expect(css).toMatch(/\.scanlines \{[^}]*isolation: isolate/);
  });

  it('keeps the scene canvas behind everything and shrinks it under 768px', () => {
    expect(css).toMatch(/\.scene-layer__canvas \{[^}]*position: fixed[^}]*z-index: -1/);
    expect(css).toMatch(/@media \(max-width: 767px\) \{[^@]*\.scene-layer__canvas \{[^}]*block-size: min\(40dvh, 260px\)/);
  });
});

// The glass rules only win if the older stylesheets stop painting the same
// surfaces, so the conflicting declarations are removed from them.
describe('older stylesheets leave the glass look to panels.css', () => {
  const read = (n: string) => readFileSync(new URL('./' + n, import.meta.url), 'utf8').replace(/\s+/g, ' ');
  const rule = (css: string, selector: string) => new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, '\\$&')} \\{([^}]*)\\}`).exec(css)?.[1] ?? '';

  it('strips surface, border, shadow, clip and animation from .panel, .deck and .interrupt', () => {
    const shell = rule(read('shell.css'), '.panel');
    const deck = rule(read('deck.css'), '.deck');
    const interrupt = rule(read('interrupt.css'), '.interrupt');
    for (const body of [shell, deck, interrupt]) {
      expect(body).not.toMatch(/background|border:|box-shadow|backdrop-filter|clip-path|animation/);
    }
  });

  it('drops the toast entrance and the violet question form override', () => {
    expect(read('shell.css')).not.toContain('toast-in');
    expect(read('interrupt.css')).not.toMatch(/\.qform \{[^}]*(border-color|box-shadow)/);
  });

  it('imports panels.css after every stylesheet it overrides', () => {
    const mainPath = '../main.tsx';
    const main = readFileSync(new URL(mainPath, import.meta.url), 'utf8');
    const at = (n: string) => main.indexOf(`./styles/${n}`);
    for (const n of ['shell.css', 'deck.css', 'interrupt.css']) {
      expect(at('panels.css')).toBeGreaterThan(at(n));
    }
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Read the stylesheets from disk: Vitest stubs CSS imports, even with ?raw.
const read = (name: string) => readFileSync(new URL(name, import.meta.url), 'utf8');
const tokens = read('./tokens.css');
const base = read('./base.css');

// The token names and values are the contract every later web task consumes;
// they come from the design spec, section 3.2.
const tokenValues: Record<string, string> = {
  '--void': 'oklch(0.12 0.05 275)',
  '--void-deep': 'oklch(0.09 0.04 275)',
  '--haze': 'oklch(0.30 0.16 300)',
  '--glass': 'oklch(0.16 0.05 265 / 0.70)',
  '--glass-solid': 'oklch(0.16 0.05 265 / 0.94)',
  '--surface': 'var(--glass)',
  '--surface-solid': 'var(--glass-solid)',
  '--surface-raised': 'oklch(0.21 0.06 262 / 0.82)',
  '--edge': 'oklch(0.86 0.17 205 / 0.35)',
  '--edge-strong': 'oklch(0.86 0.17 205 / 0.7)',
  '--cyan': 'oklch(0.86 0.17 205)',
  '--cyan-dim': 'oklch(0.86 0.17 205 / 0.35)',
  '--magenta': 'oklch(0.70 0.29 340)',
  '--magenta-dim': 'oklch(0.70 0.29 340 / 0.35)',
  '--violet': 'oklch(0.62 0.25 295)',
  '--violet-dim': 'oklch(0.62 0.25 295 / 0.3)',
  '--teal': 'oklch(0.82 0.16 170)',
  '--amber': 'oklch(0.83 0.17 75)',
  '--text': 'oklch(0.95 0.02 220)',
  '--text-muted': 'oklch(0.74 0.05 240)',
  '--blur-glass': '14px',
  // Unchanged type, spacing and timing tokens stay in the table as before.
  '--font-hud': "'Chakra Petch', system-ui, sans-serif",
  '--font-prose': "'IBM Plex Sans', system-ui, sans-serif",
  '--font-code': "'JetBrains Mono', ui-monospace, monospace",
  '--step--1': '0.8125rem',
  '--step-0': '0.9375rem',
  '--step-1': '1.125rem',
  '--step-2': '1.5rem',
  '--step-3': '2rem',
  '--space-1': '4px',
  '--space-4': '16px',
  '--space-9': '36px',
  '--radius': '6px',
  '--dur-in': '320ms',
  '--dur-out': '220ms',
};

const collapse = (css: string) => css.replace(/\s+/g, ' ');

describe('design tokens', () => {
  it('declares the spec values on :root', () => {
    expect(collapse(tokens)).toContain(':root {');
    for (const [name, value] of Object.entries(tokenValues)) {
      expect(collapse(tokens)).toContain(`${name}: ${value};`);
    }
  });

  it('builds each glow from a tight edge and an 18px halo at 35 percent', () => {
    const css = collapse(tokens);
    expect(css).toContain('--glow-cyan: 0 0 0 1px oklch(0.86 0.17 205), 0 0 18px oklch(0.86 0.17 205 / 0.35);');
    expect(css).toContain('--glow-magenta: 0 0 0 1px oklch(0.70 0.29 340), 0 0 18px oklch(0.70 0.29 340 / 0.35);');
    expect(css).toContain('--glow-violet: 0 0 0 1px oklch(0.62 0.25 295), 0 0 18px oklch(0.62 0.25 295 / 0.35);');
    expect(css).toContain('--glow-text: 0 0 8px oklch(0.86 0.17 205 / 0.6);');
  });

  it('defines the easing token', () => {
    expect(collapse(tokens)).toContain('--ease-out: cubic-bezier(0.16, 1, 0.3, 1);');
  });
});

describe('base styles', () => {
  it('sets the body type', () => {
    expect(collapse(base)).toContain('font: 400 var(--step-0)/1.6 var(--font-prose);');
  });

  it('shades the page from void at the top to haze at the horizon', () => {
    const css = collapse(base);
    expect(css).toContain('linear-gradient(180deg, var(--void) 0%, var(--void) 45%, var(--haze) 100%)');
    // The scene draws its own floor grid, so the CSS grid overlay is gone.
    expect(base).not.toContain('background-size: 36px 36px');
  });

  it('carries the styles shared across later tasks', () => {
    for (const shared of ['@keyframes blink', '.btn {', '.btn--primary', '.btn--danger', '.btn--ghost', '.chip {', '.link-button']) {
      expect(base).toContain(shared);
    }
  });

  it('honors the motion setting attributes', () => {
    expect(base).toContain("[data-motion='off']");
    expect(base).toContain("[data-motion='reduced']");
  });
});

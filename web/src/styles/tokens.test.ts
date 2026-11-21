import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Read the stylesheets from disk: Vitest stubs CSS imports, even with ?raw.
const read = (name: string) => readFileSync(new URL(name, import.meta.url), 'utf8');
const tokens = read('./tokens.css');
const base = read('./base.css');

// The token names and values are the contract every later web task consumes;
// they come from the design spec, section 3.1.
const tokenValues: Record<string, string> = {
  '--void': 'oklch(0.14 0.04 265)',
  '--void-deep': 'oklch(0.10 0.035 265)',
  '--surface': 'oklch(0.19 0.05 260 / 0.72)',
  '--surface-solid': 'oklch(0.19 0.05 260)',
  '--surface-raised': 'oklch(0.24 0.06 258 / 0.82)',
  '--edge': 'oklch(0.82 0.14 210 / 0.28)',
  '--edge-strong': 'oklch(0.82 0.14 210 / 0.6)',
  '--cyan': 'oklch(0.82 0.14 210)',
  '--cyan-dim': 'oklch(0.82 0.14 210 / 0.35)',
  '--magenta': 'oklch(0.68 0.24 330)',
  '--magenta-dim': 'oklch(0.68 0.24 330 / 0.35)',
  '--violet': 'oklch(0.60 0.20 290)',
  '--violet-dim': 'oklch(0.60 0.20 290 / 0.3)',
  '--teal': 'oklch(0.78 0.13 175)',
  '--amber': 'oklch(0.80 0.15 75)',
  '--text': 'oklch(0.93 0.02 230)',
  '--text-muted': 'oklch(0.70 0.04 240)',
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

  it('defines the glow and easing tokens', () => {
    expect(tokens).toContain('--glow-cyan:');
    expect(tokens).toContain('--glow-magenta:');
    expect(collapse(tokens)).toContain('--ease-out: cubic-bezier(0.16, 1, 0.3, 1);');
  });
});

describe('base styles', () => {
  it('sets the body type and background', () => {
    expect(collapse(base)).toContain('font: 400 var(--step-0)/1.6 var(--font-prose);');
    expect(base).toContain('var(--void);');
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

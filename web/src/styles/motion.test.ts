import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// Spec 3.9: only transform, opacity and filter animate in CSS. Those three
// stay on the compositor, so a glow or a fill has to fade on a layer of its
// own instead of animating its paint properties. Vite rewrites a literal
// path in new URL() into a served asset URL, so the directory name is passed
// through a variable, as panels.test.ts does.
const STYLES_DIR = '.';

// Every stylesheet must be read, so a new one cannot slip past the rule.
const EXPECTED_SHEETS = [
  'agents.css',
  'base.css',
  'cinematic.css',
  'deck.css',
  'hud.css',
  'interrupt.css',
  'log.css',
  'panels.css',
  'shell.css',
  'stage.css',
  'tokens.css',
];

const ALLOWED = new Set(['transform', 'opacity', 'filter']);

export interface Finding {
  sheet: string;
  rule: string;
  property: string;
  value: string;
}

export interface Scan {
  findings: Finding[];
  transitions: string[];
  keyframes: string[];
  animated: string[];
}

// splitTopLevel splits a declaration value on the commas that separate
// transition layers, leaving the commas inside cubic-bezier() and other
// functions alone.
function splitTopLevel(value: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < value.length; i++) {
    const c = value[i];
    if (c === '(') depth++;
    else if (c === ')') depth--;
    else if (c === ',' && depth === 0) {
      parts.push(value.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(value.slice(start));
  return parts.map((p) => p.trim()).filter(Boolean);
}

// blockAt returns the body of the block that opens at index and the index
// just past its closing brace.
function blockAt(text: string, open: number): { body: string; end: number } {
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    if (text[i] === '{') depth++;
    else if (text[i] === '}' && --depth === 0) return { body: text.slice(open + 1, i), end: i + 1 };
  }
  throw new Error('Unbalanced block');
}

// scanCss reports every property that a transition or a keyframe animates.
// A zero-duration visibility step is a discrete switch rather than an
// animation, so the shorthand may name it only with a literal 0s duration.
export function scanCss(sheet: string, source: string): Scan {
  const css = source.replace(/\/\*[\s\S]*?\*\//g, '');
  const scan: Scan = { findings: [], transitions: [], keyframes: [], animated: [] };

  const record = (rule: string, segment: string) => {
    const property = segment.split(/\s+/)[0].toLowerCase();
    if (property === 'none') return;
    scan.transitions.push(`${sheet} ${rule}: ${segment}`);
    if (property === 'visibility' && /^visibility\s+0m?s\b/.test(segment)) return;
    scan.animated.push(property);
    if (!ALLOWED.has(property)) scan.findings.push({ sheet, rule, property, value: segment });
  };

  for (const match of css.matchAll(/(?<![\w-])(transition|transition-property)\s*:\s*([^;}]+)/g)) {
    const [, rule, value] = match;
    for (const raw of splitTopLevel(value)) {
      record(rule, raw.replace(/!\s*important\s*$/i, '').trim());
    }
  }

  for (const match of css.matchAll(/@keyframes\s+([\w-]+)\s*\{/g)) {
    const name = match[1];
    scan.keyframes.push(name);
    const { body } = blockAt(css, match.index + match[0].length - 1);
    for (const frame of body.matchAll(/[^{}]+\{([^}]*)\}/g)) {
      for (const decl of frame[1].matchAll(/(?:^|;)\s*([\w-]+)\s*:/g)) {
        scan.animated.push(decl[1]);
        if (!ALLOWED.has(decl[1])) {
          scan.findings.push({ sheet, rule: `@keyframes ${name}`, property: decl[1], value: frame[1].trim() });
        }
      }
    }
  }

  return scan;
}

const stylesDir = fileURLToPath(new URL(STYLES_DIR, import.meta.url));
const sheets = readdirSync(stylesDir).filter((name) => name.endsWith('.css')).sort();

describe('the stylesheets animate only transform, opacity and filter', () => {
  it('reads every stylesheet under styles', () => {
    expect(sheets).toEqual(EXPECTED_SHEETS);
  });

  it('leaves no transition or keyframe on any other property', () => {
    const findings = sheets.flatMap((sheet) => scanCss(sheet, readFileSync(stylesDir + sheet, 'utf8')).findings);
    expect(findings).toEqual([]);
  });

  it('parses the transitions and keyframes the stylesheets actually carry', () => {
    const scans = sheets.map((sheet) => scanCss(sheet, readFileSync(stylesDir + sheet, 'utf8')));
    const keyframes = scans.flatMap((s) => s.keyframes);
    expect(keyframes).toEqual(expect.arrayContaining([
      'blink', 'boot-ring', 'cs-spin', 'danger-pulse', 'fade-in', 'flicker-once', 'gauge-pulse', 'power-up',
    ]));
    const transitions = scans.flatMap((s) => s.transitions).join('\n');
    // The multi-value case: the boot fade lists opacity before its
    // visibility step, and the button keeps its fill on its own layer.
    expect(transitions).toMatch(/shell\.css transition: opacity [^,]+$/m);
    expect(transitions).toMatch(/shell\.css transition: visibility 0s linear var\(--dur-out\)$/m);
    expect(transitions).toContain('transform var(--dur-out) var(--ease-out)');
    expect(transitions).toContain('opacity var(--dur-out) var(--ease-out)');
  });
});

describe('scanCss', () => {
  it('reads each value of a multi-value transition', () => {
    const scan = scanCss('x.css', '.a { transition: opacity 150ms, transform 200ms; }');
    expect(scan.animated).toEqual(['opacity', 'transform']);
    expect(scan.findings).toEqual([]);
  });

  it('fails a transition on a paint property', () => {
    const scan = scanCss('x.css', '.a { transition: box-shadow 150ms, background-color 200ms; }');
    expect(scan.findings.map((f) => f.property)).toEqual(['box-shadow', 'background-color']);
  });

  it('reads transition-property lists and fails all', () => {
    expect(scanCss('x.css', '.a { transition-property: opacity, filter; }').findings).toEqual([]);
    expect(scanCss('x.css', '.a { transition-property: all; }').findings.map((f) => f.property)).toEqual(['all']);
  });

  it('skips none and allows only a zero-duration visibility step', () => {
    expect(scanCss('x.css', '[data-motion="off"] * { transition: none !important; }').animated).toEqual([]);
    expect(scanCss('x.css', '.a { transition: visibility 0s linear 200ms; }').animated).toEqual([]);
    expect(scanCss('x.css', '.a { transition: visibility 200ms; }').findings.map((f) => f.property)).toEqual(['visibility']);
  });

  it('reads every declaration inside a keyframe body', () => {
    const css = '@keyframes glow { 0% { opacity: 0; transform: scale(0.9); } 50% { box-shadow: 0 0 8px red; } }';
    const scan = scanCss('x.css', css);
    expect(scan.keyframes).toEqual(['glow']);
    expect(scan.animated).toEqual(['opacity', 'transform', 'box-shadow']);
    expect(scan.findings.map((f) => f.property)).toEqual(['box-shadow']);
  });
});

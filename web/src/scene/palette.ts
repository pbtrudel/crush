import * as THREE from 'three';

// parseOklch converts "oklch(L C H ...)" to a THREE.Color. THREE cannot parse
// oklch() itself, and custom properties are returned as written. Returns null
// for any other syntax.
export function parseOklch(raw: string): THREE.Color | null {
  const m = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)/.exec(raw);
  if (!m) return null;
  const L = Number(m[1]);
  const h = (Number(m[3]) * Math.PI) / 180;
  const a = Number(m[2]) * Math.cos(h);
  const b = Number(m[2]) * Math.sin(h);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const mid = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const clamp = (x: number) => Math.min(1, Math.max(0, x));
  return new THREE.Color().setRGB(
    clamp(4.0767416621 * l - 3.3077115913 * mid + 0.2309699292 * s),
    clamp(-1.2684380046 * l + 2.6097574011 * mid - 0.3413193965 * s),
    clamp(-0.0041960863 * l - 0.7034186147 * mid + 1.707614701 * s),
    THREE.LinearSRGBColorSpace,
  );
}

// token reads a color token from the document, so the scene follows
// tokens.css. jsdom and a missing token fall back to the given sRGB hex,
// which is the value of the token named beside each call.
export function token(name: string, fallback: string): THREE.Color {
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return parseOklch(raw) ?? new THREE.Color().setStyle(raw || fallback);
}

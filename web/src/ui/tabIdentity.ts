// Each crush terminal gets its own tab; a stable per-project hue on the
// favicon tells them apart at a glance.
export function projectHue(dir: string): number {
  let h = 2166136261;
  for (let i = 0; i < dir.length; i++) {
    h ^= dir.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return (h >>> 0) % 360;
}

export function faviconFor(hue: number): string {
  const c = `hsl(${hue} 90% 65%)`;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><polygon points="16,2 28,9 28,23 16,30 4,23 4,9" fill="none" stroke="${c}" stroke-width="2.5"/><circle cx="16" cy="16" r="5" fill="${c}"/></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

export function tabTitle(project: string, sessionTitle: string, attention: boolean): string {
  const base = `${project} · ${sessionTitle || 'New session'}`;
  return attention ? `(!) ${base}` : base;
}

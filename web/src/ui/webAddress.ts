// webAddress keeps only a URL a browser may open; the server answers 502
// for anything else, so this is the last guard.
export function webAddress(raw: string): string | null {
  try {
    const url = new URL(raw);
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : null;
  } catch {
    return null;
  }
}

// mulberry32 is a small, fast seeded generator. The scene uses it so a
// reload with the same seed lays out the same dust and beams.
export function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const KEY_SEED = 'crush-web-seed';

// sceneSeed lets the user pin the layout from the console
// (localStorage['crush-web-seed'] = '7'), and otherwise changes per load.
export function sceneSeed(): number {
  const saved = localStorage.getItem(KEY_SEED);
  if (saved !== null) {
    const parsed = Number(saved);
    if (Number.isInteger(parsed)) return parsed;
  }
  return Date.now() >>> 0;
}

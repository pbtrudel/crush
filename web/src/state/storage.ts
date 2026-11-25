// Storage can throw: a blocked third-party context, Safari private mode or
// a full quota. A preference that cannot be read falls back to its default
// and one that cannot be written lasts for this tab.
export function readStored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function removeStored(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // Kept in memory only; see above.
  }
}

export function writeStored(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Kept in memory only; see above.
  }
}

// The gateway token for this tab. sessionStorage is isolated by origin,
// which carries the gateway's port, so another local server never sees
// it, and a reload keeps it. Storage can throw; memory is the fallback.
const KEY = 'crush-web-token';
let memory: string | null = null;

export function token(): string | null {
  if (memory !== null) return memory;
  try { memory = sessionStorage.getItem(KEY); } catch { memory = null; }
  return memory;
}

export function setToken(t: string): void {
  memory = t;
  try { sessionStorage.setItem(KEY, t); } catch { /* memory only */ }
}

export function forgetToken(): void {
  memory = null;
  try { sessionStorage.removeItem(KEY); } catch { /* nothing stored */ }
}

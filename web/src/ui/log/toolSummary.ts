const KEYS = ['command', 'file_path', 'pattern', 'path', 'url', 'query', 'prompt', 'description'];
const MAX = 120;

export function summarizeTool(_name: string, input: string): string {
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(input);
  } catch {
    return '';
  }
  for (const k of KEYS) {
    const v = parsed[k];
    if (typeof v === 'string' && v) return v.length > MAX ? `${v.slice(0, MAX)}…` : v;
  }
  return '';
}

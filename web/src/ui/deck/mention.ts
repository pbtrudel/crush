export function activeMention(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const m = /(^|\s)@([^\s@]*)$/.exec(before);
  if (!m) return null;
  return { start: caret - m[2].length - 1, query: m[2] };
}

export function applyMention(text: string, start: number, caret: number, path: string) {
  const insert = `@${path} `;
  const rest = text.slice(caret).replace(/^\s/, '');
  return { text: text.slice(0, start) + insert + rest, caret: start + insert.length };
}

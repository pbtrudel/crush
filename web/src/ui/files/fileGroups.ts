import type { FileSummary } from '../../api/types';

const slash = (p: string) => p.replace(/\\/g, '/');

export function relativePath(path: string, root: string): string {
  const p = slash(path);
  const r = slash(root).replace(/\/+$/, '');
  return r && p.startsWith(`${r}/`) ? p.slice(r.length + 1) : p;
}

export interface FileRow extends FileSummary { name: string; rel: string }

export function fileGroups(files: FileSummary[], root: string): Array<{ dir: string; files: FileRow[] }> {
  const groups = new Map<string, FileRow[]>();
  for (const f of files) {
    const rel = relativePath(f.path, root);
    const cut = rel.lastIndexOf('/');
    const dir = cut < 0 ? '' : rel.slice(0, cut);
    const row: FileRow = { ...f, rel, name: rel.slice(cut + 1) };
    groups.set(dir, [...(groups.get(dir) ?? []), row]);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([dir, rows]) => ({ dir, files: rows.sort((a, b) => a.name.localeCompare(b.name)) }));
}

// changeBlocks draws the +/- bar: one block per ten changed lines up to the
// bar's length, split between additions and removals by their share.
export function changeBlocks(additions: number, removals: number, blocks = 5): Array<'add' | 'rem' | 'none'> {
  const total = additions + removals;
  if (total === 0) return Array.from({ length: blocks }, () => 'none');
  const filled = Math.min(blocks, Math.max(1, Math.ceil(total / 10)));
  let add = Math.round((filled * additions) / total);
  // Rounding a lopsided change would otherwise erase the smaller side; when
  // both sides changed and the bar has room, each keeps one block.
  if (additions > 0 && removals > 0 && filled >= 2) add = Math.min(filled - 1, Math.max(1, add));
  return Array.from({ length: blocks }, (_, i) => (i < add ? 'add' : i < filled ? 'rem' : 'none'));
}

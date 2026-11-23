import { describe, expect, it } from 'vitest';
import type { FileSummary } from '../../api/types';
import { changeBlocks, fileGroups, relativePath } from './fileGroups';

const f = (path: string, over: Partial<FileSummary> = {}): FileSummary => ({
  path, versions: 2, version_ids: [0, 1], additions: 1, removals: 0,
  diagnostics: { error: 0, warning: 0 }, updated_at: 1, session_ids: ['s1'], ...over,
});

describe('fileGroups', () => {
  it('makes paths relative to the working directory, and keeps outside paths whole', () => {
    expect(relativePath('/repo/internal/a.go', '/repo')).toBe('internal/a.go');
    expect(relativePath('/repo/a.go', '/repo/')).toBe('a.go');
    expect(relativePath('/elsewhere/b.go', '/repo')).toBe('/elsewhere/b.go');
    expect(relativePath('C:\\repo\\web\\x.ts', 'C:\\repo')).toBe('web/x.ts');
  });

  it('groups by directory, directories and names sorted', () => {
    const groups = fileGroups([f('/repo/web/b.ts'), f('/repo/a.go'), f('/repo/web/a.ts')], '/repo');
    expect(groups.map((g) => g.dir)).toEqual(['', 'web']);
    expect(groups[1].files.map((x) => x.name)).toEqual(['a.ts', 'b.ts']);
  });
});

describe('changeBlocks', () => {
  it('fills blocks by size and splits them by kind', () => {
    expect(changeBlocks(0, 0)).toEqual(['none', 'none', 'none', 'none', 'none']);
    expect(changeBlocks(1, 0)).toEqual(['add', 'none', 'none', 'none', 'none']);
    expect(changeBlocks(30, 20)).toEqual(['add', 'add', 'add', 'rem', 'rem']);
    expect(changeBlocks(0, 400)).toEqual(['rem', 'rem', 'rem', 'rem', 'rem']);
  });

  // A lopsided change still has additions, so rounding must not erase them.
  it('keeps at least one block of each kind when both changed', () => {
    expect(changeBlocks(1, 100)).toEqual(['add', 'rem', 'rem', 'rem', 'rem']);
    expect(changeBlocks(100, 1)).toEqual(['add', 'add', 'add', 'add', 'rem']);
  });
});

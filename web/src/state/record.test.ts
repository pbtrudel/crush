import { describe, expect, it } from 'vitest';
import { without } from './record';

describe('without', () => {
  it('drops one key and keeps the others', () => {
    const before = { a: 1, b: 2 };
    const after = without(before, 'a');
    expect(after).toEqual({ b: 2 });
    expect(before).toEqual({ a: 1, b: 2 });
  });

  it('returns the same record when the key is absent', () => {
    const before: Record<string, number> = { a: 1 };
    expect(without(before, 'b')).toBe(before);
  });

  it('takes a record with optional keys', () => {
    const before: Partial<Record<'a' | 'b', unknown>> = { a: new Error('x') };
    expect(Object.keys(without(before, 'a'))).toEqual([]);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { readStored, writeStored } from './storage';

afterEach(() => vi.restoreAllMocks());

describe('storage', () => {
  it('reads and writes through localStorage', () => {
    writeStored('crush-web-test', 'x');
    expect(readStored('crush-web-test')).toBe('x');
  });

  it('never throws when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('denied', 'SecurityError'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('full', 'QuotaExceededError'); });
    expect(readStored('k')).toBeNull();
    expect(() => writeStored('k', 'v')).not.toThrow();
  });
});

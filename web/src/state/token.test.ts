import { beforeEach, describe, expect, it } from 'vitest';
import { forgetToken, setToken, token } from './token';

describe('token', () => {
  beforeEach(() => { sessionStorage.clear(); forgetToken(); });
  it('keeps the token for this tab', () => {
    setToken('abc');
    expect(token()).toBe('abc');
    expect(sessionStorage.getItem('crush-web-token')).toBe('abc');
  });
  it('forgets it', () => {
    setToken('abc');
    forgetToken();
    expect(token()).toBeNull();
    expect(sessionStorage.getItem('crush-web-token')).toBeNull();
  });
  it('survives storage that throws', () => {
    const orig = Storage.prototype.setItem;
    Storage.prototype.setItem = () => { throw new Error('quota'); };
    try { setToken('mem'); expect(token()).toBe('mem'); } finally { Storage.prototype.setItem = orig; }
  });
});

import { describe, expect, it } from 'vitest';
import { webAddress } from './webAddress';

describe('webAddress', () => {
  it('keeps an https address', () => {
    expect(webAddress('https://a.b/c')).toBe('https://a.b/c');
  });

  it('keeps an http address on the loopback', () => {
    expect(webAddress('http://localhost:1455/start')).toBe('http://localhost:1455/start');
  });

  it('refuses a script URL', () => {
    expect(webAddress('javascript:alert(1)')).toBeNull();
  });

  it('refuses a file URL', () => {
    expect(webAddress('file:///etc/passwd')).toBeNull();
  });

  it('refuses text that is not a URL', () => {
    expect(webAddress('not a url')).toBeNull();
  });
});

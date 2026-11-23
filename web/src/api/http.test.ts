import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './http';

afterEach(() => vi.unstubAllGlobals());

describe('api.health', () => {
  it('maps 401 to unauthorized, other failures to down', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })));
    await expect(api.health()).resolves.toBe('unauthorized');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 502 })));
    await expect(api.health()).resolves.toBe('down');
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')));
    await expect(api.health()).resolves.toBe('down');
  });
});

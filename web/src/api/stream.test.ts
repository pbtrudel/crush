import { beforeEach, describe, expect, it, vi } from 'vitest';
import { connectStream, type StreamEnv } from './stream';

class FakeSocket {
  static last: FakeSocket;
  onopen?: () => void; onmessage?: (e: { data: string }) => void; onclose?: (e: { code: number }) => void;
  sent: string[] = [];
  constructor(public url: string) { FakeSocket.last = this; }
  send(d: string) { this.sent.push(d); }
  close() {}
}

function env(over: Partial<StreamEnv> = {}): StreamEnv {
  return {
    url: (since) => `ws://x/api/stream${since ? `?since=${since}` : ''}`,
    WebSocket: FakeSocket as unknown as StreamEnv['WebSocket'],
    health: async () => 'ok',
    schedule: (fn) => fn(),
    setTimeout: (fn: () => void) => { fn(); return 0; },
    setInterval: () => 0,
    clearInterval: () => {},
    ...over,
  };
}

describe('connectStream', () => {
  beforeEach(() => { FakeSocket.last = undefined as unknown as FakeSocket; });

  it('batches frames, reconnects with since, and ends after closing', async () => {
    const frames: number[] = [];
    let seq = 0;
    const conns: string[] = [];
    connectStream({
      onFrames: (fs) => { fs.forEach((f) => frames.push(f.seq)); seq = fs.at(-1)!.seq; },
      onConn: (c) => conns.push(c),
      lastSeq: () => seq,
    }, env());
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const first = FakeSocket.last;
    expect(first.url).toBe('ws://x/api/stream');
    first.onopen?.();
    first.onmessage?.({ data: JSON.stringify({ seq: 5, type: 'snapshot', payload: {} }) });
    expect(frames).toEqual([5]);

    first.onclose?.({ code: 1006 });
    await vi.waitFor(() => expect(FakeSocket.last).not.toBe(first));
    expect(FakeSocket.last.url).toBe('ws://x/api/stream?since=5');
    expect(conns).toContain('reconnecting');

    const second = FakeSocket.last;
    second.onmessage?.({ data: JSON.stringify({ seq: 6, type: 'gateway_closing', payload: {} }) });
    second.onclose?.({ code: 1001 });
    expect(conns.at(-1)).toBe('ended');
  });

  it('reports unauthorized when health returns 401', async () => {
    const conns: string[] = [];
    connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 0 },
      env({ health: async () => 'unauthorized' }));
    await vi.waitFor(() => expect(conns).toEqual(['unauthorized']));
  });

  it('ends after repeated network failures', async () => {
    const conns: string[] = [];
    connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 0 },
      env({ health: async () => 'down' }));
    await vi.waitFor(() => expect(conns.at(-1)).toBe('ended'));
  });

  it('resync close 4001 reconnects without since', async () => {
    const seq = 9;
    connectStream({ onFrames: () => {}, onConn: () => {}, lastSeq: () => seq }, env());
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const s = FakeSocket.last;
    s.onclose?.({ code: 4001 });
    await vi.waitFor(() => expect(FakeSocket.last).not.toBe(s));
    expect(FakeSocket.last.url).toBe('ws://x/api/stream');
  });
});

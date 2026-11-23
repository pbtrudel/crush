import { beforeEach, describe, expect, it, vi } from 'vitest';
import { connectStream, type StreamEnv } from './stream';

class FakeSocket {
  static last: FakeSocket;
  onopen?: () => void; onmessage?: (e: { data: string }) => void; onclose?: (e: { code: number }) => void;
  sent: string[] = [];
  closed = false;
  constructor(public url: string) { FakeSocket.last = this; }
  send(d: string) { this.sent.push(d); }
  close() { this.closed = true; }
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

// queued returns an env whose timers run only when the test advances them, so
// a retry or probe chain never spins inside a synchronous stub.
function queued(over: Partial<StreamEnv> = {}) {
  const timers: { fn: () => void; ms: number }[] = [];
  const e = env({
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    ...over,
  });
  const next = async () => {
    await vi.waitFor(() => expect(timers.length).toBeGreaterThan(0));
    const t = timers.shift()!;
    t.fn();
    return t.ms;
  };
  return { env: e, timers, next };
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

  it('skips a frame that is not JSON and keeps the connection', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const frames: number[] = [];
    connectStream({ onFrames: (fs) => fs.forEach((f) => frames.push(f.seq)), onConn: () => {}, lastSeq: () => 0 }, env());
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const sock = FakeSocket.last;
    sock.onopen?.();
    expect(() => sock.onmessage?.({ data: 'not json' })).not.toThrow();
    sock.onmessage?.({ data: JSON.stringify({ seq: 2, type: 'web_error', payload: { error: 'x' } }) });
    expect(frames).toEqual([2]);
    expect(sock.closed).toBe(false);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it('skips JSON that is not a frame and keeps the connection', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const frames: number[] = [];
    connectStream({ onFrames: (fs) => fs.forEach((f) => frames.push(f.seq)), onConn: () => {}, lastSeq: () => 0 }, env());
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const sock = FakeSocket.last;
    sock.onopen?.();
    expect(() => sock.onmessage?.({ data: 'null' })).not.toThrow();
    expect(() => sock.onmessage?.({ data: '{"seq":"a"}' })).not.toThrow();
    sock.onmessage?.({ data: JSON.stringify({ seq: 3, type: 'web_error', payload: { error: 'x' } }) });
    expect(frames).toEqual([3]);
    expect(sock.closed).toBe(false);
    expect(warn).toHaveBeenCalledTimes(2);
    warn.mockRestore();
  });

  it('returns to live after a ring resume that sends no snapshot', async () => {
    const conns: string[] = [];
    connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 5 }, env());
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const first = FakeSocket.last;
    first.onopen?.();
    first.onclose?.({ code: 1006 });
    await vi.waitFor(() => expect(FakeSocket.last).not.toBe(first));
    expect(FakeSocket.last.url).toBe('ws://x/api/stream?since=5');

    const second = FakeSocket.last;
    second.onopen?.();
    second.onmessage?.({ data: JSON.stringify({ seq: 6, type: 'message', payload: {} }) });
    expect(conns).toEqual(['live', 'reconnecting', 'live']);
    expect(conns.at(-1)).toBe('live');
  });

  it('reports unauthorized when health returns 401', async () => {
    const conns: string[] = [];
    connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 0 },
      env({ health: async () => 'unauthorized' }));
    await vi.waitFor(() => expect(conns).toEqual(['unauthorized']));
  });

  it('ends after repeated network failures', async () => {
    const conns: string[] = [];
    const q = queued({ health: async () => 'down' });
    const stop = connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 0 }, q.env);
    for (let i = 0; i < 4; i++) await q.next();
    await vi.waitFor(() => expect(conns.at(-1)).toBe('ended'));
    expect(conns.filter((c) => c === 'ended')).toHaveLength(1);
    expect(q.timers).toHaveLength(1);
    expect(q.timers[0].ms).toBe(10_000);
    stop();
  });

  it('keeps probing after ending and returns to live when health recovers', async () => {
    let status: 'ok' | 'down' = 'down';
    const conns: string[] = [];
    const q = queued({ health: async () => status });
    const stop = connectStream({
      onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 4,
    }, q.env);
    for (let i = 0; i < 4; i++) await q.next();
    await vi.waitFor(() => expect(conns.at(-1)).toBe('ended'));

    status = 'ok';
    await q.next();
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    expect(FakeSocket.last.url).toBe('ws://x/api/stream?since=4');
    FakeSocket.last.onopen?.();
    expect(conns.at(-1)).toBe('live');
    stop();
  });

  it('does not probe health after gateway_closing', async () => {
    let calls = 0;
    const conns: string[] = [];
    const q = queued({ health: async () => { calls++; return 'ok'; } });
    connectStream({ onFrames: () => {}, onConn: (c) => conns.push(c), lastSeq: () => 0 }, q.env);
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const sock = FakeSocket.last;
    sock.onopen?.();
    sock.onmessage?.({ data: JSON.stringify({ seq: 6, type: 'gateway_closing', payload: {} }) });
    sock.onclose?.({ code: 1001 });
    expect(conns.at(-1)).toBe('ended');
    expect(q.timers).toHaveLength(0);
    expect(calls).toBe(1);
  });

  it('backs off when a socket opens and closes without a frame', async () => {
    const q = queued();
    const stop = connectStream({ onFrames: () => {}, onConn: () => {}, lastSeq: () => 0 }, q.env);
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const first = FakeSocket.last;
    first.onopen?.();
    first.onclose?.({ code: 1006 });
    expect(q.timers.at(-1)?.ms).toBe(500);

    await q.next();
    await vi.waitFor(() => expect(FakeSocket.last).not.toBe(first));
    const second = FakeSocket.last;
    second.onopen?.();
    second.onclose?.({ code: 1006 });
    expect(q.timers.at(-1)?.ms).toBe(1000);
    stop();
  });

  it('stop clears the ping and closes the socket', async () => {
    const cleared: unknown[] = [];
    let pings = 0;
    const stop = connectStream({ onFrames: () => {}, onConn: () => {}, lastSeq: () => 0 }, env({
      setInterval: () => { pings++; return 7; },
      clearInterval: (id) => { cleared.push(id); },
    }));
    await vi.waitFor(() => expect(FakeSocket.last).toBeDefined());
    const sock = FakeSocket.last;
    sock.onopen?.();
    expect(pings).toBe(1);
    stop();
    expect(cleared).toEqual([7]);
    expect(sock.closed).toBe(true);
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

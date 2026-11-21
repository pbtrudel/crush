import type { Frame } from './types';
import type { Conn } from '../state/reducer';
import { api } from './http';

export interface StreamHandlers {
  onFrames(frames: Frame[]): void;
  onConn(c: Conn): void;
  lastSeq(): number;
}

type SocketLike = {
  onopen?: (() => void) | null;
  onmessage?: ((e: { data: string }) => void) | null;
  onclose?: ((e: { code: number }) => void) | null;
  send(d: string): void;
  close(code?: number): void;
};

export interface StreamEnv {
  url(since: number): string;
  WebSocket: new (url: string) => SocketLike;
  health(): Promise<'ok' | 'unauthorized' | 'down'>;
  schedule(fn: () => void): void;
  setTimeout(fn: () => void, ms: number): unknown;
  setInterval(fn: () => void, ms: number): unknown;
  clearInterval(id: unknown): void;
}

const PING_MS = 15_000;
const MAX_DOWN = 5;
const CLOSE_RESYNC = 4001;

// browserEnv keeps every request same-origin (the CSP only allows
// connect-src 'self') and derives ws or wss from the page protocol.
export function browserEnv(): StreamEnv {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return {
    url: (since) => `${proto}://${location.host}/api/stream${since > 0 ? `?since=${since}` : ''}`,
    WebSocket: WebSocket as unknown as StreamEnv['WebSocket'],
    health: () => api.health(),
    schedule: (fn) => requestAnimationFrame(() => fn()),
    setTimeout: (fn, ms) => window.setTimeout(fn, ms),
    setInterval: (fn, ms) => window.setInterval(fn, ms),
    clearInterval: (id) => window.clearInterval(id as number),
  };
}

// connectStream keeps one WebSocket alive, batching frames per animation
// frame so streaming deltas cost one render each, and resumes with
// ?since=<seq> after a drop. The 15 s ping keeps the gateway's 45 s idle
// read deadline from closing a quiet connection. Returns a function that
// stops it.
export function connectStream(h: StreamHandlers, env: StreamEnv = browserEnv()): () => void {
  let stopped = false;
  let ended = false;
  let down = 0;
  let attempt = 0;
  let resync = false;
  let buffer: Frame[] = [];
  let scheduled = false;
  let ping: unknown = null;
  let ws: SocketLike | null = null;

  const flush = () => {
    scheduled = false;
    const batch = buffer;
    buffer = [];
    if (batch.length) h.onFrames(batch);
  };

  const retry = () => {
    if (stopped || ended) return;
    h.onConn('reconnecting');
    const delay = Math.min(10_000, 500 * 2 ** attempt++);
    env.setTimeout(() => void open(), delay);
  };

  const open = async () => {
    if (stopped) return;
    const status = await env.health();
    if (stopped) return;
    if (status === 'unauthorized') { h.onConn('unauthorized'); return; }
    if (status === 'down') {
      if (++down >= MAX_DOWN) { ended = true; h.onConn('ended'); return; }
      retry();
      return;
    }
    down = 0;
    const since = resync ? 0 : h.lastSeq();
    resync = false;
    const sock = new env.WebSocket(env.url(since));
    ws = sock;
    sock.onopen = () => {
      attempt = 0;
      ping = env.setInterval(() => sock.send('{"type":"ping"}'), PING_MS);
    };
    sock.onmessage = (e) => {
      if (stopped) return;
      const f = JSON.parse(e.data) as Frame;
      if (f.type === 'gateway_closing') ended = true;
      buffer.push(f);
      if (!scheduled) { scheduled = true; env.schedule(flush); }
    };
    sock.onclose = (e) => {
      if (stopped) return;
      if (ping !== null) env.clearInterval(ping);
      flush();
      if (ended) { h.onConn('ended'); return; }
      if (e.code === CLOSE_RESYNC) resync = true;
      retry();
    };
  };

  void open();
  return () => {
    stopped = true;
    if (ping !== null) env.clearInterval(ping);
    ws?.close();
  };
}

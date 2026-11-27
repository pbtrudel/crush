import type { Frame } from './types';
import type { Conn } from '../state/reducer';
import { api } from './http';
import { token } from '../state/token';

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
  WebSocket: new (url: string, protocols: string[]) => SocketLike;
  health(): Promise<'ok' | 'unauthorized' | 'down'>;
  schedule(fn: () => void): void;
  setTimeout(fn: () => void, ms: number): unknown;
  setInterval(fn: () => void, ms: number): unknown;
  clearInterval(id: unknown): void;
}

// streamProtocols offers the stream protocol, plus the token in a
// subprotocol entry: a browser cannot set an Authorization header on a
// WebSocket upgrade.
export function streamProtocols(): string[] {
  const t = token();
  return t ? ['crush.v1', 'crush.auth.' + t] : ['crush.v1'];
}

const PING_MS = 15_000;
const MAX_DOWN = 5;
const PROBE_MS = 10_000;
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
  // terminal is set by gateway_closing: the gateway is gone for good, so no
  // retry or health probe may run afterwards. unauthorized is equally final.
  let terminal = false;
  let unauthorized = false;
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
    if (stopped || terminal || unauthorized) return;
    h.onConn('reconnecting');
    const delay = Math.min(10_000, 500 * 2 ** attempt++);
    env.setTimeout(() => void open(), delay);
  };

  const open = async () => {
    if (stopped || terminal || unauthorized) return;
    const status = await env.health();
    if (stopped || terminal || unauthorized) return;
    if (status === 'unauthorized') {
      unauthorized = true;
      h.onConn('unauthorized');
      return;
    }
    if (status === 'down') {
      down++;
      if (down < MAX_DOWN) {
        retry();
        return;
      }
      if (down === MAX_DOWN) h.onConn('ended');
      // A sleep, a closed lid or a gateway restart must not strand the tab:
      // keep probing at the capped backoff and resume when health returns.
      env.setTimeout(() => void open(), PROBE_MS);
      return;
    }
    down = 0;
    const since = resync ? 0 : h.lastSeq();
    resync = false;
    const sock = new env.WebSocket(env.url(since), streamProtocols());
    ws = sock;
    // A socket that receives data earns a fast retry if it then drops. A
    // socket that opens and closes without a frame keeps backing off, so the
    // counter resets on the first frame rather than on open.
    let gotFrame = false;
    sock.onopen = () => {
      if (stopped || terminal || unauthorized) return;
      // Live means the socket is established, not that a frame arrived. A
      // resume inside the replay ring gets no snapshot, and a resume at the
      // head gets no frames at all, so the open socket is the only signal
      // that the tab is current again.
      h.onConn('live');
      ping = env.setInterval(() => sock.send('{"type":"ping"}'), PING_MS);
    };
    sock.onmessage = (e) => {
      if (stopped) return;
      if (!gotFrame) { gotFrame = true; attempt = 0; }
      let f: Frame;
      try {
        f = JSON.parse(e.data) as Frame;
      } catch {
        // A frame the page cannot read is dropped.
        console.warn('Dropped a frame the page could not read');
        return;
      }
      // Valid JSON of the wrong shape is not a frame either.
      if (!f || typeof f !== 'object' || typeof f.seq !== 'number' || typeof f.type !== 'string') {
        console.warn('Dropped a frame the page could not read');
        return;
      }
      if (f.type === 'gateway_closing') terminal = true;
      buffer.push(f);
      if (!scheduled) { scheduled = true; env.schedule(flush); }
    };
    sock.onclose = (e) => {
      if (stopped) return;
      if (ping !== null) env.clearInterval(ping);
      ping = null;
      flush();
      if (terminal) { h.onConn('ended'); return; }
      if (unauthorized) return;
      if (e.code === CLOSE_RESYNC) resync = true;
      retry();
    };
  };

  void open();
  return () => {
    stopped = true;
    if (ping !== null) env.clearInterval(ping);
    ping = null;
    ws?.close();
  };
}

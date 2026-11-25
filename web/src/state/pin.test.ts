import { afterEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame, Message, Snapshot } from '../api/types';
import { ApiError, api } from '../api/http';
import { applyBrowse } from './pin';
import { initialState, needsAttention, reduce, type State } from './reducer';
import { actions, handleFrames, state } from './store';

const fx = fixtures as unknown as Record<string, Frame>;

function snap(over: Partial<Snapshot> = {}): Snapshot {
  return { ...(fx.snapshot.payload as Snapshot), ...over };
}

// live is the tab after the fixture snapshot: the terminal on s1.
function live(): State {
  return reduce(initialState(), fx.snapshot);
}

function message(sessionId: string, id: string): Message {
  return {
    id, role: 'assistant', session_id: sessionId, parts: [{ type: 'text', data: { text: 'Hi' } }],
    model: '', provider: '', created_at: 1, updated_at: 1,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

afterEach(() => {
  vi.restoreAllMocks();
  state.value = initialState();
});

describe('applyBrowse', () => {
  it('shows the browsed session from its snapshot', () => {
    const browsed = snap({
      current_session_id: 's-2',
      session: {
        id: 's-2', parent_session_id: '', title: 'Tokenizer port', message_count: 1,
        prompt_tokens: 0, completion_tokens: 0, cost: 0, is_busy: false, created_at: 0, updated_at: 0,
      },
      messages: [message('s-2', 'browse-1')],
      files: [{
        path: '/repo/tokenizer.go', versions: 1, version_ids: [0], additions: 1, removals: 0,
        diagnostics: { error: 0, warning: 0 }, updated_at: 1, session_ids: ['s-2'], version_tools: [],
      }],
    });
    const s = applyBrowse(live(), browsed);
    expect(s.browsing).toBe('s-2');
    expect(s.sessionId).toBe('s-2');
    expect(s.session?.title).toBe('Tokenizer port');
    expect(s.messages.map((m) => m.id)).toEqual(['browse-1']);
    expect(s.files.map((f) => f.path)).toEqual(['/repo/tokenizer.go']);
  });

  it('keeps what the browse snapshot does not carry', () => {
    const before = {
      ...live(),
      awsSso: { command: 'aws sso login', url: 'https://x' },
      answeredHere: { p1: true as const },
      announced: { 'stale-config': true as const },
      planReady: 's1',
    };
    const after = applyBrowse(before, snap({ current_session_id: 's-2' }));
    expect(after.awsSso).toEqual({ command: 'aws sso login', url: 'https://x' });
    expect(after.answeredHere).toEqual({ p1: true });
    expect(after.announced).toEqual({ 'stale-config': true });
    expect(after.planReady).toBe('s1');
    expect(after.terminalSession).toBe('s1');
  });
});

describe('browsing session frames', () => {
  it('keeps the browsed session on session_follow', () => {
    let s = applyBrowse(live(), snap({ current_session_id: 's-2' }));
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session_follow', payload: { session_id: 's-3' } });
    expect(s.sessionId).toBe('s-2');
    expect(s.browsing).toBe('s-2');
    expect(s.terminalSession).toBe('s-3');
  });

  it('re-pins when the terminal follows the browsed session', () => {
    let s = applyBrowse(live(), snap({ current_session_id: 's-2' }));
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session_follow', payload: { session_id: 's-2' } });
    expect(s.browsing).toBeNull();
    expect(s.sessionId).toBe('s-2');
    expect(s.terminalSession).toBe('s-2');
  });

  it('still shows a permission while unpinned', () => {
    const s = reduce(
      { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' },
      { ...fx.permission_request, seq: 500 },
    );
    expect(needsAttention(s)).toBe(true);
  });

  it('reloads the browsed session after a reconnect snapshot', () => {
    const s = reduce(
      { ...live(), browsing: 's-2', sessionId: 's-2', terminalSession: 's1' },
      { seq: 900, type: 'snapshot', payload: fx.snapshot.payload },
    );
    expect(s.sessionId).toBe('s-2');
    expect(s.browsing).toBe('s-2');
    expect(s.terminalSession).toBe('s1');
    expect(s.needsReload).toBe('s-2');
  });

  it('keeps the terminal session frames out of the browsed transcript', () => {
    let s = applyBrowse(live(), snap({ current_session_id: 's-2', messages: [] }));
    s = reduce(s, { seq: s.lastSeq + 1, type: 'message', payload: { type: 'created', payload: message('s1', 'terminal-1') } });
    expect(s.messages).toHaveLength(0);
    s = reduce(s, { seq: s.lastSeq + 1, type: 'message', payload: { type: 'updated', payload: message('s-2', 'browse-1') } });
    expect(s.messages.map((m) => m.id)).toEqual(['browse-1']);
  });

  it('keeps the browsed session frames out of the terminal transcript', () => {
    let s = live();
    s = reduce(s, { seq: s.lastSeq + 1, type: 'message', payload: { type: 'created', payload: message('s-2', 'browse-1') } });
    expect(s.messages.map((m) => m.id)).not.toContain('browse-1');
  });
});

describe('root session count', () => {
  it('seeds the count from the snapshot', () => {
    expect(live().rootSessions).toBe(1);
  });

  it('counts a root session the terminal creates', () => {
    let s = { ...live(), rootSessions: 1 };
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session', payload: { type: 'created', payload: { id: 's-9', parent_session_id: '' } } });
    expect(s.rootSessions).toBe(2);
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session', payload: { type: 'created', payload: { id: 'sub-9', parent_session_id: 's1' } } });
    expect(s.rootSessions).toBe(2);
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session', payload: { type: 'deleted', payload: { id: 's-9', parent_session_id: '' } } });
    expect(s.rootSessions).toBe(1);
  });

  it('leaves the count alone on an update or a child session', () => {
    let s = { ...live(), rootSessions: 2 };
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session', payload: { type: 'updated', payload: { id: 's1', parent_session_id: '' } } });
    expect(s.rootSessions).toBe(2);
    s = reduce(s, { seq: s.lastSeq + 1, type: 'session', payload: { type: 'deleted', payload: { id: 'sub-1', parent_session_id: 's1' } } });
    expect(s.rootSessions).toBe(2);
  });
});

describe('actions', () => {
  it('browse loads the session snapshot and applies it', async () => {
    const snapshot = vi.spyOn(api, 'snapshot').mockResolvedValue(snap({ current_session_id: 's-2' }));
    state.value = live();
    await expect(actions.browse('s-2')).resolves.toBeNull();
    expect(snapshot).toHaveBeenCalledWith('s-2');
    expect(state.value.browsing).toBe('s-2');
    expect(state.value.sessionId).toBe('s-2');
    expect(state.value.terminalSession).toBe('s1');
  });

  it('browse reports a failure and leaves the view where it was', async () => {
    vi.spyOn(api, 'snapshot').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' };
    await expect(actions.browse('s-3')).resolves.toEqual({ tone: 'error', text: 'gateway on fire' });
    expect(state.value.browsing).toBe('s-2');
    expect(state.value.sessionId).toBe('s-2');
  });

  it('a stale browse answer never overwrites a newer choice', async () => {
    const first = deferred<Snapshot>();
    const second = deferred<Snapshot>();
    vi.spyOn(api, 'snapshot').mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    state.value = live();
    const a = actions.browse('s-2');
    const b = actions.browse('s-3');
    second.resolve(snap({ current_session_id: 's-3' }));
    await b;
    first.resolve(snap({ current_session_id: 's-2' }));
    await a;
    expect(state.value.sessionId).toBe('s-3');
    expect(state.value.browsing).toBe('s-3');
  });

  it('re-pin loads the terminal session', async () => {
    const snapshot = vi.spyOn(api, 'snapshot').mockResolvedValue(snap({ current_session_id: 's1' }));
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2', terminalSession: 's1' };
    await expect(actions.repin()).resolves.toBeNull();
    expect(snapshot).toHaveBeenCalledWith();
    expect(state.value.browsing).toBeNull();
    expect(state.value.sessionId).toBe('s1');
  });

  it('a late browse answer does not overwrite a re-pin', async () => {
    const browsing = deferred<Snapshot>();
    const terminal = deferred<Snapshot>();
    vi.spyOn(api, 'snapshot').mockReturnValueOnce(browsing.promise).mockReturnValueOnce(terminal.promise);
    state.value = live();
    const b = actions.browse('s-2');
    const r = actions.repin();
    terminal.resolve(snap({ current_session_id: 's1' }));
    await r;
    browsing.resolve(snap({ current_session_id: 's-2' }));
    await b;
    expect(state.value.browsing).toBeNull();
    expect(state.value.sessionId).toBe('s1');
  });

  it('re-pin failure keeps the browsed session', async () => {
    vi.spyOn(api, 'snapshot').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' };
    await expect(actions.repin()).resolves.toEqual({ tone: 'error', text: 'gateway on fire' });
    expect(state.value.browsing).toBe('s-2');
  });

  it('opens the browsed session in the terminal, then re-pins on the frame', async () => {
    const follow = vi.spyOn(api, 'follow').mockResolvedValue(undefined);
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' };
    await expect(actions.openBrowsedInTerminal()).resolves.toBeNull();
    expect(follow).toHaveBeenCalledWith('s-2');
    expect(state.value.browsing).toBe('s-2');
    handleFrames([{ seq: 901, type: 'session_follow', payload: { session_id: 's-2' } }]);
    expect(state.value.browsing).toBeNull();
    expect(state.value.sessionId).toBe('s-2');
  });

  it('reloads the browsed session after a reconnect without asking again', async () => {
    const snapshot = vi.spyOn(api, 'snapshot').mockResolvedValue(snap({ current_session_id: 's-2' }));
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' };
    handleFrames([fx.snapshot]);
    await flush();
    expect(snapshot).toHaveBeenCalledWith('s-2');
    expect(state.value.sessionId).toBe('s-2');
    expect(state.value.needsReload).toBeNull();
  });

  it('drops a browsed reload answer after the user returned', async () => {
    const reload = deferred<Snapshot>();
    vi.spyOn(api, 'snapshot').mockImplementation((sid?: string) =>
      sid === 's-2' ? reload.promise : Promise.resolve(snap({ current_session_id: 's1' })));
    state.value = { ...applyBrowse(live(), snap({ current_session_id: 's-2' })), browsing: 's-2' };
    handleFrames([fx.snapshot]);
    // Return while the reload is in flight; its answer belongs to the old view.
    await actions.repin();
    reload.resolve(snap({ current_session_id: 's-2' }));
    await flush();
    expect(state.value.browsing).toBeNull();
    expect(state.value.sessionId).toBe('s1');
  });
});

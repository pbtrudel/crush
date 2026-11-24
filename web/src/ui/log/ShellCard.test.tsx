import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { ApiError, api } from '../../api/http';
import { initialState, reduce, type ShellRun } from '../../state/reducer';
import { dispatch, state } from '../../state/store';
import { SHELL_CARD_TTL_MS, ShellCard } from './ShellCard';

const root = document.createElement('div');

function run(patch: Partial<ShellRun> = {}): ShellRun {
  return { sessionId: 's1', command: 'make test', output: '', done: false, exitCode: null, error: null, ...patch };
}

function seed(shells: Record<string, ShellRun> = {}) {
  state.value = { ...initialState(), conn: 'live', sessionId: 's1', shells };
}

async function mount() {
  await act(() => { render(<ShellCard />, root); });
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function outputFrame(runId: string, payload: Record<string, unknown>) {
  return { seq: 1, type: 'shell_output', payload: { run_id: runId, session_id: 's1', ...payload } };
}

function cancelButton() {
  return [...root.querySelectorAll('button')].find((b) => b.textContent === 'Cancel') ?? null;
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  vi.useRealTimers();
  vi.restoreAllMocks();
  state.value = initialState();
});

describe('ShellCard', () => {
  it('streams output and cancels the run', async () => {
    seed({ r1: run({ output: 'first\n' }) });
    const cancel = vi.spyOn(api, 'cancelShell').mockResolvedValue(undefined);
    await mount();

    expect(root.textContent).toContain('$ make test');
    expect(root.querySelector('.shell-card__output')!.textContent).toBe('first\n');

    await act(() => {
      dispatch((s) => reduce(s, outputFrame('r1', { chunk: 'second\n' })));
    });
    expect(root.querySelector('.shell-card__output')!.textContent).toBe('first\nsecond\n');

    const button = cancelButton()!;
    button.focus();
    await act(() => { button.click(); });
    await flush();
    expect(cancel).toHaveBeenCalledWith('r1');

    await act(() => {
      dispatch((s) => reduce(s, outputFrame('r1', { done: true, exit_code: 3 })));
    });

    expect(cancelButton()).toBeNull();
    expect(root.textContent).toContain('$ make test');
    expect(root.textContent).toContain('Exit 3');
    // Cancel held the focus when it unmounted, so the card takes it; focus
    // never falls to the body.
    expect(document.activeElement).toBe(root.querySelector('.shell-card'));
  });

  it('keeps Cancel focused while the cancel is in flight', async () => {
    seed({ r1: run() });
    const pending = deferred<void>();
    const cancel = vi.spyOn(api, 'cancelShell').mockReturnValue(pending.promise);
    await mount();

    const button = cancelButton()!;
    button.focus();
    await act(() => { button.click(); });
    await flush();

    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(button.matches(':disabled')).toBe(false);
    expect(document.activeElement).toBe(button);

    await act(() => { button.click(); });
    await flush();
    expect(cancel).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve();
      await pending.promise;
    });
    expect(button.getAttribute('aria-disabled')).toBeNull();
  });

  it('removes a card whose cancel answers unknown_run', async () => {
    // The gateway no longer holds the run, so no done frame will come and
    // the card has nothing left to show.
    seed({ r1: run() });
    vi.spyOn(api, 'cancelShell').mockRejectedValue(new ApiError(404, 'unknown_run', 'no such run'));
    await mount();

    const button = cancelButton()!;
    await act(() => { button.click(); });
    await flush();

    expect(root.querySelector('[role="alert"]')).toBeNull();
    expect(root.querySelector('.shell-card__failure')).toBeNull();
    expect(root.querySelector('.shell-card')).toBeNull();
    expect(state.value.shells.r1).toBeUndefined();
  });

  it('leaves the focus alone when Cancel did not hold it', async () => {
    seed({ r1: run({ output: 'a\n' }) });
    await mount();
    const well = root.querySelector('.shell-card__output') as HTMLElement;
    well.focus();

    await act(() => {
      dispatch((s) => reduce(s, outputFrame('r1', { done: true, exit_code: 0 })));
    });

    expect(document.activeElement).toBe(well);
    expect(document.activeElement).not.toBe(document.body);
  });

  it('shows a cancel failure inline', async () => {
    seed({ r1: run() });
    vi.spyOn(api, 'cancelShell').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await mount();

    await act(() => { cancelButton()!.click(); });
    await flush();

    expect(root.querySelector('[role="alert"]')!.textContent).toBe('gateway on fire');
  });

  it('shows the failure line of a finished run', async () => {
    seed({ r1: run({ done: true, exitCode: 1, error: 'The shell command failed' }) });
    await mount();

    expect(root.querySelector('.shell-card__failure')!.textContent).toBe('The shell command failed');
    expect(cancelButton()).toBeNull();
  });

  it('announces the exit in a polite status', async () => {
    seed({ r1: run({ done: true, exitCode: 0 }) });
    await mount();

    const status = root.querySelector('[role="status"]')!;
    expect(status.textContent).toBe('Exit 0');
  });

  it('renders a run from another tab as a plain shell command', async () => {
    seed({ r9: run({ command: '' }) });
    await mount();

    expect(root.textContent).toContain('Shell command');
    expect(root.textContent).not.toContain('$ ');
  });

  it('leaves the runs of another session out', async () => {
    seed({ r2: run({ sessionId: 's2' }) });
    await mount();
    expect(root.querySelector('.shell-card')).toBeNull();
  });

  it('does not put the streaming well in a live region', async () => {
    seed({ r1: run({ output: 'a\n' }) });
    await mount();

    const well = root.querySelector('.shell-card__output')!;
    expect(well.getAttribute('aria-live')).toBeNull();
    expect(well.closest('[aria-live]')).toBeNull();
  });

  it('drops a finished card 30 seconds after it finishes', async () => {
    vi.useFakeTimers();
    seed({ r1: run({ done: true, exitCode: 0 }) });
    await mount();
    expect(root.querySelector('.shell-card')).not.toBeNull();

    await act(() => { vi.advanceTimersByTime(SHELL_CARD_TTL_MS - 1); });
    expect(root.querySelector('.shell-card')).not.toBeNull();

    await act(() => { vi.advanceTimersByTime(1); });
    expect(root.querySelector('.shell-card')).toBeNull();
    expect(state.value.shells.r1).toBeUndefined();
  });

  it('drops the card when the persisted shell message arrives', async () => {
    seed({ r1: run() });
    await mount();
    expect(root.querySelector('.shell-card')).not.toBeNull();

    await act(() => {
      dispatch((s) => reduce(s, outputFrame('r1', { done: true, exit_code: 0 })));
    });
    expect(root.querySelector('.shell-card')).not.toBeNull();

    await act(() => {
      dispatch((s) => reduce(s, {
        seq: 2,
        type: 'message',
        payload: {
          type: 'created',
          payload: {
            id: 'm-shell', role: 'user', session_id: 's1',
            parts: [{ type: 'shell_command', data: { command: 'make test', output: 'ok\n', exit_code: 0 } }],
            model: '', provider: '', created_at: 0, updated_at: 0,
          },
        },
      }));
    });

    expect(root.querySelector('.shell-card')).toBeNull();
  });
});

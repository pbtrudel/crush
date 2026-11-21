import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { api } from '../../api/http';
import { initialState } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { CommandDeck, MAX_ATTACHMENT_BYTES } from './CommandDeck';

type Files = { files: string[]; truncated: boolean };

const root = document.createElement('div');

function seed(patch: Partial<typeof state.value> = {}) {
  state.value = { ...initialState(), conn: 'live', sessionId: 's1', ...patch };
}

async function mount() {
  await act(() => {
    render(<CommandDeck />, root);
  });
}

function textarea() {
  return root.querySelector<HTMLTextAreaElement>('#deck-input')!;
}

function transmit() {
  return Array.from(root.querySelectorAll('button')).find((b) => b.type === 'submit')!;
}

function listbox() {
  return root.querySelector('#deck-suggest');
}

function error() {
  return root.querySelector('#deck-error')!;
}

// jsdom does not run form submission from a button click, so the Transmit
// click path is exercised as the submit event that click would fire.
function submitForm() {
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

function key(name: string, keyCode?: number) {
  const e = new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true });
  if (keyCode !== undefined) Object.defineProperty(e, 'keyCode', { value: keyCode });
  textarea().dispatchEvent(e);
}

async function type(value: string, caret = value.length) {
  const el = textarea();
  await act(() => {
    el.value = value;
    el.setSelectionRange(caret, caret);
    el.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
}

async function attach(...files: File[]) {
  const picker = root.querySelector<HTMLInputElement>('input[type="file"]')!;
  Object.defineProperty(picker, 'files', { value: files, configurable: true });
  await act(() => {
    picker.dispatchEvent(new Event('change', { bubbles: true }));
  });
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

// The completion requests are debounced with a timer; the fake clock moves
// that timer without a real-time wait.
async function settle() {
  await act(() => {
    vi.advanceTimersByTime(120);
  });
  await act(async () => {
    await Promise.resolve();
  });
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('CommandDeck', () => {
  it('disables transmit and explains the missing session', async () => {
    seed({ sessionId: '' });
    await mount();
    expect(transmit().disabled).toBe(true);
    expect(root.querySelector('label[for="deck-input"]')).not.toBeNull();
    expect(root.querySelector('#deck-help')!.textContent).toBe('Start or open a session in the terminal.');
  });

  it('keeps the newer completion when a stale response arrives late', async () => {
    seed();
    const slow = deferred<Files>();
    const fast = deferred<Files>();
    const complete = vi
      .spyOn(api, 'completeFiles')
      .mockReturnValueOnce(slow.promise)
      .mockReturnValueOnce(fast.promise);
    await mount();

    await type('read @ol');
    await settle();
    expect(complete).toHaveBeenCalledTimes(1);

    await type('read @new');
    await settle();
    expect(complete).toHaveBeenCalledTimes(2);

    await act(async () => {
      fast.resolve({ files: ['src/new.ts'], truncated: false });
      await fast.promise;
    });
    expect(listbox()!.textContent).toContain('src/new.ts');

    await act(async () => {
      slow.resolve({ files: ['src/old.ts'], truncated: false });
      await slow.promise;
    });
    expect(listbox()!.textContent).toContain('src/new.ts');
    expect(listbox()!.textContent).not.toContain('src/old.ts');
  });

  it('closes the suggestion list on Escape before cancelling a run', async () => {
    seed({ busy: true });
    vi.spyOn(api, 'completeFiles').mockResolvedValue({ files: ['src/a.ts', 'src/b.ts'], truncated: false });
    const cancel = vi.spyOn(actions, 'cancel').mockResolvedValue(undefined);
    await mount();

    await type('read @a');
    await settle();
    expect(listbox()).not.toBeNull();

    await act(() => {
      key('Escape');
    });
    expect(listbox()).toBeNull();
    expect(cancel).not.toHaveBeenCalled();

    await act(() => {
      key('Escape');
    });
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('inserts the picked path in place of the mention', async () => {
    seed();
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      cb(0);
      return 0;
    });
    vi.spyOn(api, 'completeFiles').mockResolvedValue({ files: ['src/app.ts'], truncated: false });
    await mount();

    await type('read @ap now', 8);
    await settle();
    const option = root.querySelector<HTMLElement>('[role="option"]')!;
    expect(option.textContent).toBe('src/app.ts');
    expect(option.getAttribute('aria-selected')).toBe('true');
    await act(() => {
      option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
    });
    expect(textarea().value).toBe('read @src/app.ts now');
    expect(listbox()).toBeNull();
  });

  it('rejects attachments that push the upload over the 32 MiB cap', async () => {
    seed();
    await mount();
    const big = new File(['x'], 'big.bin');
    Object.defineProperty(big, 'size', { value: MAX_ATTACHMENT_BYTES + 1 });
    await attach(big);
    expect(error().textContent).toBe('Attachments must total under 32 MiB.');
    expect(root.querySelectorAll('.deck__files li')).toHaveLength(0);
  });

  it('sends only once when Enter or a click repeats before the prompt is accepted', async () => {
    seed();
    const pending = deferred<string | null>();
    const send = vi.spyOn(actions, 'sendPrompt').mockReturnValueOnce(pending.promise);
    await mount();
    await type('run the tests');

    await act(() => {
      key('Enter');
      key('Enter');
      submitForm();
    });
    await flush();
    expect(send).toHaveBeenCalledTimes(1);
    expect(transmit().disabled).toBe(true);

    await act(async () => {
      pending.resolve(null);
      await pending.promise;
    });
    await flush();
    expect(textarea().value).toBe('');
    expect(transmit().disabled).toBe(false);

    await type('run them again');
    send.mockResolvedValueOnce(null);
    await act(() => {
      submitForm();
    });
    await flush();
    expect(send).toHaveBeenCalledTimes(2);
    expect(send.mock.calls[1][0]).toBe('run them again');
  });

  it('ignores the Enter that ends an IME composition', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue(null);
    vi.spyOn(api, 'completeFiles').mockResolvedValue({ files: ['src/a.ts'], truncated: false });
    await mount();

    await type('read @a');
    await settle();
    expect(listbox()).not.toBeNull();

    await act(() => {
      key('Enter', 229);
    });
    expect(send).not.toHaveBeenCalled();
    expect(listbox()).not.toBeNull();
    expect(textarea().value).toBe('read @a');
  });

  it('reserves multipart overhead against the cap at send time', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue(null);
    await mount();
    const file = new File(['x'], 'large.bin');
    Object.defineProperty(file, 'size', { value: MAX_ATTACHMENT_BYTES - 100 * 1024 });
    await attach(file);
    expect(root.querySelectorAll('.deck__files li')).toHaveLength(1);

    const prompt = 'x'.repeat(50 * 1024);
    await type(prompt);
    await act(() => {
      key('Enter');
    });
    await flush();

    expect(send).not.toHaveBeenCalled();
    expect(error().textContent).toBe('Attachments must total under 32 MiB.');
    expect(textarea().value).toBe(prompt);
  });

  it('keeps the prompt and attachments when the send fails', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue('Prompt not sent: The request did not reach crush');
    await mount();
    await type('summarize');
    await attach(new File(['x'], 'notes.txt'));
    expect(root.querySelector('.deck__files')!.textContent).toContain('notes.txt');

    await act(() => {
      key('Enter');
    });
    await flush();

    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0]).toBe('summarize');
    expect(send.mock.calls[0][1]).toHaveLength(1);
    expect(textarea().value).toBe('summarize');
    expect(root.querySelector('.deck__files')!.textContent).toContain('notes.txt');
    expect(error().textContent).toBe('Prompt not sent: The request did not reach crush');
  });

  it('clears the prompt and attachments after a successful send', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue(null);
    await mount();
    await type('summarize');
    await attach(new File(['x'], 'notes.txt'));

    await act(() => {
      key('Enter');
    });
    await flush();

    expect(send).toHaveBeenCalledTimes(1);
    expect(textarea().value).toBe('');
    expect(root.querySelector('.deck__files')).toBeNull();
    expect(error().textContent).toBe('');
  });
});

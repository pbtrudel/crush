import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { ApiError, api } from '../../api/http';
import { initialState } from '../../state/reducer';
import { STARTING_TEXT, type Failure } from '../../state/failure';
import { closePalette, deckMode, paletteOpen } from '../../state/palette';
import { actions, state } from '../../state/store';
import { CommandDeck, MAX_ATTACHMENT_BYTES, shellFromPalette } from './CommandDeck';

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

function modeBadge() {
  return root.querySelector('.deck__mode-badge');
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
  it('names the deck as a landmark', async () => {
    seed();
    await mount();
    // A form with an accessible name is a landmark, so the deck's input,
    // label and help text sit inside one instead of loose in the page.
    expect(root.querySelector('form.deck')!.getAttribute('aria-label')).toBe('Command deck');
  });

  it('badges the deck while plan mode is on', async () => {
    seed({ inputMode: 'plan' });
    await mount();
    const badge = root.querySelector('.deck__plan-badge')!;
    expect(badge.textContent).toContain('Plan mode');
    expect(badge.getAttribute('role')).toBeNull();
  });

  it('hides the plan badge in code mode', async () => {
    seed({ inputMode: 'code' });
    await mount();
    expect(root.querySelector('.deck__plan-badge')).toBeNull();
  });

  it('starts a session from the landing screen', async () => {
    seed({ sessionId: '' });
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue(null);
    await mount();
    expect(transmit().disabled).toBe(false);
    expect(root.querySelector('#deck-help')!.textContent).toContain('Enter to send');
    await type('first task');

    await act(() => {
      key('Enter');
    });
    await flush();

    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0]).toBe('first task');
    expect(textarea().value).toBe('');
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
    const pending = deferred<Failure | null>();
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
    expect(error().textContent).toBe('The prompt and its attachments must total under 32 MiB.');
    expect(textarea().value).toBe(prompt);
  });

  it('keeps the prompt and attachments when the send fails', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue({ tone: 'error', text: 'The request did not reach crush' });
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
    expect(error().textContent).toBe('The request did not reach crush');
    expect(error().getAttribute('role')).toBe('alert');
  });

  it('shows a starting state with a retry that resends the kept prompt', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt')
      .mockResolvedValueOnce({ tone: 'starting', text: STARTING_TEXT })
      .mockResolvedValueOnce(null);
    await mount();
    await type('run the tests');
    await attach(new File(['x'], 'notes.txt'));

    await act(() => { key('Enter'); });
    await flush();

    expect(error().getAttribute('role')).toBe('status');
    expect(error().getAttribute('data-tone')).toBe('starting');
    expect(error().textContent).toContain(STARTING_TEXT);
    expect(textarea().value).toBe('run the tests');

    await act(async () => { root.querySelector<HTMLButtonElement>('.deck__retry')!.click(); });
    await flush();

    expect(send).toHaveBeenCalledTimes(2);
    expect(send.mock.calls[1][0]).toBe('run the tests');
    expect(send.mock.calls[1][1]).toHaveLength(1);
    expect(textarea().value).toBe('');
  });

  it('shows a quiet starting line above the input while the agent boots', async () => {
    seed({ agentReady: false });
    await mount();
    const line = root.querySelector<HTMLElement>('.deck__starting')!;
    expect(line.textContent).toBe('Crush is starting the agent');
    expect(line.getAttribute('role')).toBe('status');
    // The server answers for itself, so the deck stays usable.
    expect(transmit().disabled).toBe(false);
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

  it('ignores dropped and pasted files once the session ended', async () => {
    seed({ conn: 'ended' });
    await mount();
    const drop = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [new File(['x'], 'a.txt')] } });
    await act(() => { root.querySelector('form')!.dispatchEvent(drop); });
    const paste = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(paste, 'clipboardData', { value: { files: [new File(['x'], 'p.png', { type: 'image/png' })] } });
    await act(() => { textarea().dispatchEvent(paste); });
    expect(root.querySelector('.deck__files')).toBeNull();
    expect(error().textContent).toBe('');
  });

  it('closes the suggestions when the caret leaves the mention', async () => {
    seed();
    vi.spyOn(api, 'completeFiles').mockResolvedValue({ files: ['parser.go'], truncated: false });
    await mount();
    await type('see @pa');
    await settle();
    expect(listbox()).not.toBeNull();
    textarea().setSelectionRange(0, 0);
    await act(() => { textarea().dispatchEvent(new KeyboardEvent('keyup', { key: 'Home', bubbles: true })); });
    await settle();
    expect(listbox()).toBeNull();
  });
});

describe('deck modes', () => {
  beforeEach(() => {
    document.body.append(root);
  });

  afterEach(() => {
    deckMode.value = null;
    closePalette();
    root.remove();
  });

  it('sends a bang command to the shell route', async () => {
    seed();
    deckMode.value = null;
    const shell = vi.spyOn(api, 'shell').mockResolvedValue({ run_id: 'r1' });
    await mount();
    await type('!ls -la');
    expect(modeBadge()!.textContent).toBe('Shell');

    await act(() => { key('Enter'); });
    await flush();

    expect(shell).toHaveBeenCalledWith('s1', 'ls -la');
    expect(state.value.shells.r1).toMatchObject({ sessionId: 's1', command: 'ls -la', done: false });
    expect(textarea().value).toBe('');
  });

  it('shows the shell badge only in shell mode', async () => {
    seed();
    await mount();
    expect(modeBadge()).toBeNull();
    expect(transmit().textContent).toBe('Transmit');

    await type('!ls');
    const badge = modeBadge()!;
    expect(badge.textContent).toBe('Shell');
    expect(badge.getAttribute('data-mode')).toBe('shell');
    expect(root.querySelector('#deck-help')!.textContent).toBe('Run a shell command in the workspace');
    expect(transmit().textContent).toBe('Run');

    await type('ls');
    expect(modeBadge()).toBeNull();
    expect(root.querySelector('#deck-help')!.textContent).toContain('Enter to send');
    expect(transmit().textContent).toBe('Transmit');
  });

  it('keeps attachments out of a shell command', async () => {
    seed();
    vi.spyOn(api, 'shell').mockResolvedValue({ run_id: 'r1' });
    await mount();
    await attach(new File(['x'], 'notes.txt'));
    await type('!make test');

    await act(() => { key('Enter'); });
    await flush();

    expect(root.querySelector('.deck__files')!.textContent).toContain('notes.txt');
  });

  it('keeps a shell command and reports a failure inline', async () => {
    seed();
    vi.spyOn(api, 'shell').mockRejectedValue(new ApiError(500, 'http_error', 'gateway on fire'));
    await mount();
    await type('!ls');

    await act(() => { key('Enter'); });
    await flush();

    expect(textarea().value).toBe('!ls');
    expect(error().textContent).toBe('gateway on fire');
    expect(error().getAttribute('role')).toBe('alert');
    expect(error().getAttribute('data-tone')).toBe('error');
  });

  it('types a slash into a note instead of opening the palette', async () => {
    seed();
    deckMode.value = 'note';
    await mount();

    const slash = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true });
    textarea().dispatchEvent(slash);

    expect(slash.defaultPrevented).toBe(false);
    expect(paletteOpen.value).toBeNull();
  });

  it('adds a note in note mode and leaves the mode on Escape', async () => {
    seed();
    deckMode.value = 'note';
    const note = vi.spyOn(api, 'addNote').mockResolvedValue(undefined);
    await mount();

    const badge = modeBadge()!;
    expect(badge.textContent).toBe('Note');
    expect(badge.getAttribute('data-mode')).toBe('note');
    expect(root.querySelector('#deck-help')!.textContent).toBe('Kept for the next prompt; starts no run');
    expect(transmit().textContent).toBe('Keep note');

    await type('prefer tests');
    await act(() => { key('Enter'); });
    await flush();

    expect(note).toHaveBeenCalledWith('s1', 'prefer tests');
    expect(state.value.toasts.map((t) => t.text)).toContain('Note kept for the next prompt');
    expect(textarea().value).toBe('');
    expect(deckMode.value).toBe('note');
    expect(modeBadge()!.textContent).toBe('Note');

    await type('kept draft');
    await act(() => { key('Escape'); });
    expect(deckMode.value).toBeNull();
    expect(textarea().value).toBe('kept draft');
    expect(modeBadge()).toBeNull();
  });

  it('does not cancel a busy run on the Escape that leaves note mode', async () => {
    seed({ busy: true });
    deckMode.value = 'note';
    const cancel = vi.spyOn(actions, 'cancel').mockResolvedValue(undefined);
    await mount();
    await type('draft');

    await act(() => { key('Escape'); });

    expect(deckMode.value).toBeNull();
    expect(cancel).not.toHaveBeenCalled();
    expect(textarea().value).toBe('draft');
  });

  it('keeps a note draft and shows the starting line while the agent boots', async () => {
    seed();
    deckMode.value = 'note';
    vi.spyOn(api, 'addNote').mockRejectedValue(new ApiError(503, 'agent_starting', 'the agent is starting'));
    await mount();
    await type('prefer tests');

    await act(() => { key('Enter'); });
    await flush();

    expect(textarea().value).toBe('prefer tests');
    expect(error().textContent).toContain(STARTING_TEXT);
    expect(error().getAttribute('role')).toBe('status');
    expect(error().getAttribute('data-tone')).toBe('starting');
  });

  it('puts a bang in a draft from the palette', async () => {
    seed();
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0; });
    await mount();
    await type('make test');

    await act(() => { shellFromPalette(); });

    expect(textarea().value).toBe('!make test');
    expect(modeBadge()!.textContent).toBe('Shell');
    expect(document.activeElement).toBe(textarea());
    expect(textarea().selectionStart).toBe('!make test'.length);

    // A second visit leaves the only bang in place.
    await act(() => { shellFromPalette(); });
    expect(textarea().value).toBe('!make test');
  });

  it('fills an empty deck with a bang from the palette', async () => {
    seed();
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0; });
    await mount();

    await act(() => { shellFromPalette(); });

    expect(textarea().value).toBe('!');
    expect(modeBadge()!.textContent).toBe('Shell');
    expect(document.activeElement).toBe(textarea());
    expect(textarea().selectionStart).toBe(1);
  });

  it('leaves note mode when the palette starts a shell command', async () => {
    seed();
    deckMode.value = 'note';
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0; });
    await mount();
    await type('unfinished');

    await act(() => { shellFromPalette(); });

    expect(deckMode.value).toBeNull();
    expect(textarea().value).toBe('!unfinished');
    expect(modeBadge()!.textContent).toBe('Shell');
  });
});

describe('browsing another session', () => {
  function byText(text: string) {
    return Array.from(root.querySelectorAll('button')).find((b) => b.textContent === text);
  }

  it('shows why it is read-only and both ways out', async () => {
    seed({ browsing: 's-2', terminalSession: 's1' });
    await mount();

    expect(textarea().readOnly).toBe(true);
    expect(root.textContent).toContain(
      "Prompts go to the terminal's session. Return to it, or open this session in the terminal.",
    );
    expect(byText("Return to the terminal's session")).not.toBeUndefined();
    expect(byText('Open this session in the terminal')).not.toBeUndefined();
  });

  it('does not send on Enter or a submit while browsing', async () => {
    seed({ browsing: 's-2', terminalSession: 's1' });
    const send = vi.spyOn(actions, 'sendPrompt');
    const shell = vi.spyOn(actions, 'runShell');
    await mount();
    await type('Ship it');

    key('Enter');
    submitForm();
    await flush();

    expect(send).not.toHaveBeenCalled();
    expect(shell).not.toHaveBeenCalled();
  });

  it('returns to the terminal session', async () => {
    seed({ browsing: 's-2', terminalSession: 's1' });
    const repin = vi.spyOn(actions, 'repin').mockResolvedValue(null);
    await mount();

    await act(() => { byText("Return to the terminal's session")!.click(); });

    expect(repin).toHaveBeenCalled();
  });

  it('opens the browsed session in the terminal', async () => {
    seed({ browsing: 's-2', terminalSession: 's1' });
    const open = vi.spyOn(actions, 'openBrowsedInTerminal').mockResolvedValue(null);
    await mount();

    await act(() => { byText('Open this session in the terminal')!.click(); });

    expect(open).toHaveBeenCalled();
  });
});

describe('browsing failures', () => {
  it('shows a refused return inline', async () => {
    seed({ browsing: 's-2', terminalSession: 's1' });
    vi.spyOn(actions, 'repin').mockResolvedValue({ tone: 'error', text: 'Gateway on fire' });
    await mount();

    await act(() => {
      Array.from(root.querySelectorAll('button')).find((b) => b.textContent === "Return to the terminal's session")!.click();
    });
    await flush();

    expect(root.querySelector('#deck-error')!.textContent).toBe('Gateway on fire');
  });
});

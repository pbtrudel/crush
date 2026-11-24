import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, type JSX } from 'preact';
import { act } from 'preact/test-utils';
import { ApiError, api } from '../../api/http';
import type { SecurePrompt } from '../../api/types';
import { initialState, reduce } from '../../state/reducer';
import { actions, dispatch, state } from '../../state/store';
import { Interrupt } from './Interrupt';
import { SecurePromptDialog } from './SecurePrompt';

const root = document.createElement('div');

const pass: SecurePrompt = {
  source: 'pinentry', id: 'p1', kind: 'passphrase', title: 'GPG passphrase',
  message: 'Unlock the signing key',
  details: ['Key: ABCDEF', 'Holder: <img src=x onerror=alert(1)>'],
};

const confirm: SecurePrompt = {
  source: 'ssh', id: 's2', kind: 'confirm', title: 'Host key', message: 'Trust github.com?',
};

function seed(patch: Partial<typeof state.value> = {}) {
  state.value = { ...initialState(), conn: 'live', sessionId: 's1', ...patch };
}

async function mount(el: JSX.Element) {
  await act(() => { render(el, root); });
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"]');
}

function input() {
  return root.querySelector<HTMLInputElement>('input[type="password"]');
}

function fieldLabel() {
  return root.querySelector<HTMLLabelElement>('label[for="sp-secret"]');
}

function button(name: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => (b.textContent ?? '').trim() === name)!;
}

function alertBox() {
  return root.querySelector<HTMLElement>('[role="alert"]');
}

// jsdom runs a form's submission for a submit button click, but the Enter
// path is the submit event that key would fire.
function submit() {
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

async function type(value: string) {
  const el = input()!;
  await act(() => {
    el.value = value;
    el.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
}

async function press(name: string) {
  await act(() => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true }));
  });
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => { resolve = res; });
  return { promise, resolve };
}

beforeEach(() => {
  document.body.append(root);
  seed();
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  root.remove();
  vi.restoreAllMocks();
  state.value = initialState();
});

describe('SecurePromptDialog', () => {
  it('names the dialog and describes it with the prompt text', async () => {
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    const d = dialog()!;
    expect(d.getAttribute('aria-modal')).toBe('true');
    expect(root.querySelector(`#${d.getAttribute('aria-labelledby')}`)!.textContent).toBe('GPG passphrase');
    expect(root.querySelector(`#${d.getAttribute('aria-describedby')}`)!.textContent).toBe('Unlock the signing key');
    expect(d.classList.contains('glass--alert')).toBe(true);
    expect(d.getAttribute('data-danger')).toBe('high');
  });

  it('takes the focus to the passphrase field', async () => {
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    expect(document.activeElement).toBe(input());
    expect(input()!.type).toBe('password');
    expect(input()!.getAttribute('autocomplete')).toBe('off');
    expect(fieldLabel()!.textContent).toBe('Passphrase');
  });

  it('labels a pin and a password prompt in their own words', async () => {
    await mount(<SecurePromptDialog prompt={{ ...pass, kind: 'pin', id: 'p2' }} disabled={false} />);
    expect(fieldLabel()!.textContent).toBe('PIN');
    await act(() => {
      render(<SecurePromptDialog prompt={{ ...pass, source: 'ssh', kind: 'password', id: 's1' }} disabled={false} />, root);
    });
    expect(fieldLabel()!.textContent).toBe('Password');
  });

  it('clears the passphrase before the answer settles', async () => {
    const pending = deferred<null>();
    const spy = vi.spyOn(actions, 'answerSecure').mockReturnValue(pending.promise);
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    await type('hunter2');
    await act(() => { submit(); });
    expect(input()!.value).toBe('');
    expect(spy).toHaveBeenCalledWith(pass, 'hunter2');
    expect(JSON.stringify(state.value)).not.toContain('hunter2');
    expect(root.innerHTML).not.toContain('hunter2');
    await flush();
    pending.resolve(null);
  });

  it('does not carry the typed text into the prompt that replaces it', async () => {
    const answer = vi.spyOn(actions, 'answerSecure');
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    await type('hunter2');
    await act(() => {
      render(<SecurePromptDialog prompt={{ ...pass, id: 'p2', retry: 'Wrong passphrase' }} disabled={false} />, root);
    });
    expect(input()!.value).toBe('');
    expect(answer).not.toHaveBeenCalled();
  });

  it('keeps the field empty after a failed answer', async () => {
    vi.spyOn(actions, 'answerSecure').mockResolvedValue({ tone: 'error', text: 'The request did not reach crush' });
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    await type('hunter2');
    await act(() => { submit(); });
    await flush();
    expect(alertBox()!.textContent).toContain('The request did not reach crush');
    expect(input()!.value).toBe('');
    expect(document.body.innerHTML).not.toContain('hunter2');
  });

  it('renders key details as text', async () => {
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    const details = root.querySelectorAll('.sp__details li');
    expect(Array.from(details).map((li) => li.textContent)).toEqual([
      'Key: ABCDEF', 'Holder: <img src=x onerror=alert(1)>',
    ]);
    expect(root.querySelector('img')).toBeNull();
  });

  it('confirms an SSH question without typed text', async () => {
    const spy = vi.spyOn(actions, 'answerSecure').mockResolvedValue(null);
    await mount(<SecurePromptDialog prompt={confirm} disabled={false} />);
    expect(input()).toBeNull();
    expect(fieldLabel()).toBeNull();
    expect(document.activeElement).toBe(button('Confirm'));
    await act(() => { button('Confirm').click(); });
    expect(spy).toHaveBeenCalledWith(confirm, '');
  });

  it('declines a confirm prompt with Decline', async () => {
    const spy = vi.spyOn(actions, 'cancelSecure').mockResolvedValue(null);
    await mount(<SecurePromptDialog prompt={confirm} disabled={false} />);
    await act(() => { button('Decline').click(); });
    expect(spy).toHaveBeenCalledWith(confirm);
  });

  it('cancels with Escape', async () => {
    const spy = vi.spyOn(actions, 'cancelSecure').mockResolvedValue(null);
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    await press('Escape');
    expect(spy).toHaveBeenCalledWith(pass);
  });

  it('announces why the last attempt failed', async () => {
    await mount(<SecurePromptDialog prompt={{ ...pass, retry: 'Bad passphrase (try 2 of 3)' }} disabled={false} />);
    expect(alertBox()!.textContent).toContain('Bad passphrase (try 2 of 3)');
    // The retry line sits above the field it is about.
    expect(alertBox()!.compareDocumentPosition(input()!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('sends one answer for a double Enter', async () => {
    const pending = deferred<null>();
    const spy = vi.spyOn(actions, 'answerSecure').mockReturnValue(pending.promise);
    await mount(<SecurePromptDialog prompt={pass} disabled={false} />);
    await type('x');
    await act(() => {
      submit();
      submit();
    });
    expect(spy).toHaveBeenCalledTimes(1);
    await flush();
    pending.resolve(null);
  });

  it('keeps the field but refuses to send while the link is down', async () => {
    const answer = vi.spyOn(actions, 'answerSecure');
    const cancel = vi.spyOn(actions, 'cancelSecure');
    await mount(<SecurePromptDialog prompt={pass} disabled />);
    await type('x');
    await act(() => { submit(); });
    await act(() => { button('Cancel').click(); });
    await press('Escape');
    expect(answer).not.toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
    expect(input()!.value).toBe('x');
    expect(button('Unlock').getAttribute('aria-disabled')).toBe('true');
    expect(button('Cancel').getAttribute('aria-disabled')).toBe('true');
    expect(root.querySelector('[role="status"]')!.textContent).toBe('Reconnecting to crush');
  });

  it('recovers its buttons and the focus when the link returns', async () => {
    await mount(<SecurePromptDialog prompt={pass} disabled />);
    document.body.focus();
    await act(() => {
      render(<SecurePromptDialog prompt={pass} disabled={false} />, root);
    });
    expect(button('Unlock').hasAttribute('aria-disabled')).toBe(false);
    expect(root.querySelector('[role="status"]')).toBeNull();
    expect(dialog()!.contains(document.activeElement)).toBe(true);
  });

  it('never offers to answer a touch prompt', async () => {
    const answer = vi.spyOn(actions, 'answerSecure');
    const cancel = vi.spyOn(actions, 'cancelSecure');
    await mount(<SecurePromptDialog
      prompt={{ source: 'ssh', id: 't1', kind: 'touch', title: 'Security key', message: 'Tap your security key to continue.' }}
      disabled={false}
    />);
    expect(input()).toBeNull();
    expect(root.querySelector('button')).toBeNull();
    expect(root.textContent).toContain('Tap your security key to continue.');
    await press('Escape');
    expect(answer).not.toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
  });

  it('closes and toasts when the terminal answers first', async () => {
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [pass] } }));
    await mount(<Interrupt />);
    expect(dialog()!.getAttribute('aria-labelledby')).toBe('sp-title');
    expect(root.querySelector('#sp-title')!.textContent).toBe('GPG passphrase');
    await act(() => {
      dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } }));
    });
    expect(dialog()).toBeNull();
    expect(state.value.toasts.at(-1)?.text).toBe('Answered in the terminal');
  });

  it('shows the secure prompt before a permission request', async () => {
    const permission = {
      id: 'perm-1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
      description: 'Run the tests', action: 'execute', params: {}, path: '/tmp/project',
    };
    seed({ permissions: [permission] });
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [pass] } }));
    await mount(<Interrupt />);
    expect(root.querySelector('#sp-title')!.textContent).toBe('GPG passphrase');
    expect(root.querySelector('#perm-title')).toBeNull();
  });

  it('does not render a touch-only prompt in the interrupt layer', async () => {
    const touch = { source: 'ssh' as const, id: 't1', kind: 'touch' as const, title: 'Security key', message: 'Tap your security key to continue.' };
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [touch] } }));
    await mount(<Interrupt />);
    expect(dialog()).toBeNull();
  });
});

describe('secure prompt actions', () => {
  it('marks the prompt answered before the request leaves', async () => {
    const pending = deferred<void>();
    vi.spyOn(api, 'answerSecure').mockReturnValue(pending.promise);
    const spy = vi.spyOn(actions, 'answerSecure');
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [pass] } }));
    const answering = actions.answerSecure(pass, 'x');
    expect(state.value.answeredHere[pass.id]).toBe(true);
    expect(spy).toHaveBeenCalledWith(pass, 'x');
    pending.resolve();
    expect(await answering).toBeNull();
  });

  it('sends the secret to the route of its source', async () => {
    const spy = vi.spyOn(api, 'answerSecure').mockResolvedValue(undefined);
    expect(await actions.answerSecure(pass, 'hunter2')).toBeNull();
    expect(spy).toHaveBeenCalledWith('pinentry', 'p1', 'hunter2');
  });

  it('stays answerable from the terminal after a failed answer', async () => {
    vi.spyOn(api, 'answerSecure').mockRejectedValue(new ApiError(500, 'internal', 'boom'));
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [pass] } }));
    const failure = await actions.answerSecure(pass, 'x');
    expect(failure).toEqual({ tone: 'error', text: 'boom' });
    expect(state.value.answeredHere[pass.id]).toBeUndefined();
    const after = reduce(state.value, { seq: state.value.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } });
    expect(after.toasts.at(-1)?.text).toBe('Answered in the terminal');
  });

  it('treats a prompt that left while answering as already handled', async () => {
    vi.spyOn(api, 'answerSecure').mockRejectedValue(
      new ApiError(409, 'not_pending', 'The prompt was already answered or has expired.'),
    );
    dispatch((s) => reduce(s, { seq: s.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [pass] } }));
    expect(await actions.answerSecure(pass, 'x')).toBeNull();
    expect(state.value.toasts.at(-1)).toMatchObject({
      tone: 'info', text: 'The prompt was already answered or has expired.',
    });
    // Still marked locally, so the frame that removes it stays quiet.
    expect(state.value.answeredHere[pass.id]).toBe(true);
    const after = reduce(state.value, { seq: state.value.lastSeq + 1, type: 'secure_prompt', payload: { prompts: [] } });
    expect(after.toasts.filter((t) => t.text === 'Answered in the terminal')).toHaveLength(0);
  });

  it('cancels through the route of the prompt source', async () => {
    const spy = vi.spyOn(api, 'cancelSecure').mockResolvedValue(undefined);
    expect(await actions.cancelSecure(confirm)).toBeNull();
    expect(spy).toHaveBeenCalledWith('ssh', 's2');
    expect(state.value.answeredHere[confirm.id]).toBe(true);
  });

  it('re-arms a cancel that never reached the workspace', async () => {
    vi.spyOn(api, 'cancelSecure').mockRejectedValue(new ApiError(503, 'agent_starting', 'not ready'));
    expect(await actions.cancelSecure(pass)).toEqual({
      tone: 'starting', text: 'Crush is starting the agent. Try again in a moment.',
    });
    expect(state.value.answeredHere[pass.id]).toBeUndefined();
  });

  it('treats a cancel of a prompt that left as already handled', async () => {
    vi.spyOn(api, 'cancelSecure').mockRejectedValue(
      new ApiError(409, 'not_pending', 'The prompt was already answered or has expired.'),
    );
    expect(await actions.cancelSecure(pass)).toBeNull();
    expect(state.value.toasts.at(-1)?.text).toBe('The prompt was already answered or has expired.');
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { PermissionRequest, QuestionRequest } from '../../api/types';
import { initialState } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { Interrupt } from './Interrupt';

const root = document.createElement('div');

function permission(over: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
    description: 'Run the tests', action: 'execute',
    params: { command: 'go test ./...' }, path: '/tmp/project', ...over,
  };
}

function batch(over: Partial<QuestionRequest> = {}): QuestionRequest {
  return {
    id: 'b1', session_id: 's1', tool_call_id: 'tc1',
    questions: [{
      id: 'q1', type: 'single_choice', question: 'Theme?',
      choices: [{ id: 'cyan', label: 'Cyan' }, { id: 'magenta', label: 'Magenta' }],
    }],
    ...over,
  };
}

function freeText(): QuestionRequest {
  return batch({ questions: [{ id: 'q1', type: 'free_text', question: 'Notes?' }] });
}

function seed(patch: Partial<typeof state.value> = {}) {
  state.value = { ...initialState(), conn: 'live', sessionId: 's1', ...patch };
}

async function mount() {
  await act(() => {
    render(<Interrupt />, root);
  });
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="alertdialog"], [role="dialog"]');
}

function buttons() {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('.interrupt__actions button'));
}

function allow() {
  return buttons().find((b) => b.textContent?.includes('Allow once'))!;
}

function errorBox() {
  return root.querySelector<HTMLElement>('.interrupt__error');
}

function area() {
  return root.querySelector<HTMLTextAreaElement>('textarea')!;
}

function key(name: string, init: KeyboardEventInit = {}, target: EventTarget = window) {
  target.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true, ...init }));
}

function submitForm() {
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

async function type(text: string) {
  const el = area();
  await act(() => {
    el.value = text;
    el.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => { resolve = res; });
  return { promise, resolve };
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  document.body.append(root);
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  root.remove();
  vi.restoreAllMocks();
});

describe('Interrupt', () => {
  it('renders nothing when no interrupt is pending', async () => {
    seed();
    await mount();
    expect(root.innerHTML).toBe('');
  });

  it('shows the permission alertdialog, labels it and focuses Allow once', async () => {
    seed({ permissions: [permission()] });
    await mount();
    const d = dialog()!;
    expect(d.getAttribute('role')).toBe('alertdialog');
    expect(d.getAttribute('aria-modal')).toBe('true');
    const title = d.getAttribute('aria-labelledby')!;
    expect(root.querySelector(`#${title}`)!.textContent).toBe('bash');
    const desc = d.getAttribute('aria-describedby')!;
    expect(root.querySelector(`#${desc}`)!.textContent).toBe('Run the tests');
    // No empty alert node while there is no error to announce.
    expect(root.querySelector('[role="alert"]')).toBeNull();
    expect(document.activeElement).toBe(allow());
  });

  it('counts the permissions that are still waiting', async () => {
    seed({ permissions: [permission(), permission({ id: 'p2', tool_call_id: 'tc2' })] });
    await mount();
    expect(root.querySelector('.hud-label')!.textContent).toContain('1 more waiting');
  });

  it('grants once when A repeats while the grant is in flight', async () => {
    seed({ permissions: [permission()] });
    const pending = deferred<string | null>();
    const grant = vi.spyOn(actions, 'grant').mockReturnValueOnce(pending.promise);
    await mount();
    await act(() => {
      key('a');
      key('a');
    });
    expect(grant).toHaveBeenCalledTimes(1);
    expect(grant).toHaveBeenCalledWith('p1', 'allow');
    expect(allow().disabled).toBe(true);
    await act(async () => {
      pending.resolve(null);
      await pending.promise;
    });
    expect(errorBox()).toBeNull();
  });

  it('ignores modified, repeated and composing keys', async () => {
    seed({ permissions: [permission()] });
    const grant = vi.spyOn(actions, 'grant').mockResolvedValue(null);
    await mount();
    await act(() => {
      key('a', { ctrlKey: true });
      key('a', { metaKey: true });
      key('a', { altKey: true });
      key('a', { repeat: true });
      key('a', { isComposing: true });
      key('s', { ctrlKey: true });
    });
    expect(grant).not.toHaveBeenCalled();
  });

  it('shows a grant error inline and re-enables the buttons', async () => {
    seed({ permissions: [permission()] });
    const failure = 'Permission not granted: The request did not reach crush';
    vi.spyOn(actions, 'grant').mockResolvedValue(failure);
    await mount();
    await act(() => {
      key('a');
    });
    await flush();
    expect(errorBox()!.textContent).toBe(failure);
    expect(errorBox()!.getAttribute('role')).toBe('alert');
    expect(allow().disabled).toBe(false);
  });

  it('wraps Tab from the last control to the first and Shift+Tab back', async () => {
    seed({ permissions: [permission()] });
    await mount();
    const list = buttons();
    const first = list[0];
    const last = list[list.length - 1];
    await act(() => {
      last.focus();
      key('Tab', {}, last);
    });
    expect(document.activeElement).toBe(first);
    await act(() => {
      key('Tab', { shiftKey: true }, first);
    });
    expect(document.activeElement).toBe(last);
  });

  it('refuses to send an incomplete batch and keeps the form', async () => {
    seed({ question: batch() });
    const answer = vi.spyOn(actions, 'answer');
    await mount();
    const d = dialog()!;
    expect(d.getAttribute('role')).toBe('dialog');
    expect(d.getAttribute('aria-modal')).toBe('true');
    expect(root.querySelector(`#${d.getAttribute('aria-labelledby')}`)).not.toBeNull();
    await act(() => {
      submitForm();
    });
    await flush();
    expect(answer).not.toHaveBeenCalled();
    expect(errorBox()!.textContent).toBe('Answer "Theme?" before sending.');
  });

  it('cancels once on Escape and ignores decision shortcuts in the form', async () => {
    seed({ question: freeText() });
    const grant = vi.spyOn(actions, 'grant').mockResolvedValue(null);
    const answer = vi.spyOn(actions, 'answer');
    const cancel = vi.spyOn(actions, 'cancelQuestion').mockResolvedValue(null);
    await mount();
    await type('draft');
    await act(() => {
      key('a', {}, area());
    });
    expect(grant).not.toHaveBeenCalled();
    await act(() => {
      key('Escape', {}, area());
      key('Escape', {}, area());
    });
    await flush();
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(cancel).toHaveBeenCalledWith('b1');
    expect(answer).not.toHaveBeenCalled();
  });

  it('keeps the draft when the answer is rejected', async () => {
    seed({ question: freeText() });
    const failure = 'Answers not sent: The request did not reach crush';
    const answer = vi.spyOn(actions, 'answer').mockResolvedValue(failure);
    await mount();
    await type('keep me');
    await act(() => {
      submitForm();
    });
    await flush();
    expect(answer).toHaveBeenCalledTimes(1);
    expect(answer.mock.calls[0][1]).toEqual([{ question_id: 'q1', fill_in_text: 'keep me' }]);
    expect(errorBox()!.textContent).toBe(failure);
    expect(area().value).toBe('keep me');
  });

  it('answers once when submit repeats in flight', async () => {
    seed({ question: freeText() });
    const pending = deferred<string | null>();
    const answer = vi.spyOn(actions, 'answer').mockReturnValueOnce(pending.promise);
    await mount();
    await type('go');
    await act(() => {
      submitForm();
      submitForm();
    });
    expect(answer).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve(null);
      await pending.promise;
    });
  });

  it('keeps the dialog up with its controls disabled while the gateway is away', async () => {
    seed({ conn: 'reconnecting', permissions: [permission()] });
    const grant = vi.spyOn(actions, 'grant').mockResolvedValue(null);
    await mount();
    expect(dialog()).not.toBeNull();
    expect(buttons().every((b) => b.disabled)).toBe(true);
    await act(() => {
      key('a');
    });
    expect(grant).not.toHaveBeenCalled();
    await act(() => {
      state.value = { ...state.value, conn: 'live' };
    });
    expect(buttons().every((b) => !b.disabled)).toBe(true);
  });

  it('disables the question controls while the gateway is away', async () => {
    seed({ conn: 'ended', question: batch() });
    await mount();
    expect(dialog()).not.toBeNull();
    expect(buttons().length).toBeGreaterThan(0);
    expect(buttons().every((b) => b.disabled)).toBe(true);
  });

  it('returns focus to the opener when the request goes away', async () => {
    seed({ permissions: [permission()] });
    const opener = document.createElement('button');
    opener.textContent = 'deck';
    document.body.append(opener);
    opener.focus();
    await mount();
    expect(document.activeElement).toBe(allow());
    await act(() => {
      state.value = { ...state.value, permissions: [] };
    });
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });
  it('re-enables the form and shows the error when skipping fails', async () => {
    seed({ question: freeText() });
    const failure = 'Question not cancelled: The request did not reach crush';
    const cancel = vi
      .spyOn(actions, 'cancelQuestion')
      .mockResolvedValueOnce(failure)
      .mockResolvedValueOnce(null);
    await mount();
    await act(() => {
      key('Escape');
    });
    await flush();
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(errorBox()!.textContent).toBe(failure);
    expect(buttons().every((b) => !b.disabled)).toBe(true);
    await act(() => {
      key('Escape');
    });
    await flush();
    expect(cancel).toHaveBeenCalledTimes(2);
  });

  it('does not swallow decision keys typed outside the dialog', async () => {
    seed({ conn: 'reconnecting', permissions: [permission()] });
    const grant = vi.spyOn(actions, 'grant').mockResolvedValue(null);
    await mount();
    const outside = document.createElement('textarea');
    document.body.append(outside);

    const disconnected = new KeyboardEvent('keydown', { key: 'a', bubbles: true, cancelable: true });
    await act(() => {
      outside.dispatchEvent(disconnected);
    });
    expect(disconnected.defaultPrevented).toBe(false);
    expect(grant).not.toHaveBeenCalled();

    await act(() => {
      state.value = { ...state.value, conn: 'live' };
    });
    const connected = new KeyboardEvent('keydown', { key: 'a', bubbles: true, cancelable: true });
    await act(() => {
      outside.dispatchEvent(connected);
    });
    expect(connected.defaultPrevented).toBe(false);
    expect(grant).not.toHaveBeenCalled();
    outside.remove();
  });

  it('does nothing on Escape in the permission dialog', async () => {
    seed({ permissions: [permission()] });
    const grant = vi.spyOn(actions, 'grant').mockResolvedValue(null);
    const cancel = vi.spyOn(actions, 'cancelQuestion').mockResolvedValue(null);
    await mount();
    await act(() => {
      key('Escape');
      key('Escape', {}, dialog()!);
    });
    await flush();
    expect(grant).not.toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
    expect(dialog()).not.toBeNull();
  });

  it('resets the in-flight guard when the request changes', async () => {
    seed({ permissions: [permission()] });
    const first = deferred<string | null>();
    const grant = vi
      .spyOn(actions, 'grant')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(null);
    await mount();
    await act(() => {
      key('a');
    });
    expect(grant.mock.calls[0]).toEqual(['p1', 'allow']);

    await act(() => {
      state.value = { ...state.value, permissions: [permission({ id: 'p2', tool_call_id: 'tc2' })] };
    });
    await act(() => {
      key('a');
    });
    expect(grant).toHaveBeenCalledTimes(2);
    expect(grant.mock.calls[1]).toEqual(['p2', 'allow']);

    await act(async () => {
      first.resolve('Permission not granted: stale');
      await first.promise;
    });
    await flush();
    expect(errorBox()).toBeNull();
  });

  it('clears the error when the request changes', async () => {
    seed({ permissions: [permission()] });
    vi.spyOn(actions, 'grant').mockResolvedValue('Permission not granted: nope');
    await mount();
    await act(() => {
      key('a');
    });
    await flush();
    expect(errorBox()!.textContent).toBe('Permission not granted: nope');
    await act(() => {
      state.value = { ...state.value, permissions: [permission({ id: 'p2', tool_call_id: 'tc2' })] };
    });
    expect(errorBox()).toBeNull();
  });

  it('resets the question form when the batch changes', async () => {
    seed({ question: freeText() });
    const first = deferred<string | null>();
    const answer = vi
      .spyOn(actions, 'answer')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(null);
    await mount();
    await type('go');
    await act(() => {
      submitForm();
    });
    expect(answer).toHaveBeenCalledTimes(1);
    expect(answer.mock.calls[0][0]).toBe('b1');

    await act(() => {
      state.value = {
        ...state.value,
        question: batch({
          id: 'b2', tool_call_id: 'tc2',
          questions: [{ id: 'q2', type: 'free_text', question: 'More?' }],
        }),
      };
    });
    expect(area().value).toBe('');
    await type('next');
    await act(() => {
      submitForm();
    });
    expect(answer).toHaveBeenCalledTimes(2);
    expect(answer.mock.calls[1][0]).toBe('b2');

    await act(async () => {
      first.resolve('Answers not sent: stale');
      await first.promise;
    });
    await flush();
    expect(errorBox()).toBeNull();
  });

  it('re-arms and refocuses the permission dialog when the connection recovers', async () => {
    seed({ permissions: [permission()] });
    vi.spyOn(actions, 'grant').mockResolvedValue(null);
    await mount();
    await act(() => {
      key('a');
    });
    await flush();
    expect(allow().disabled).toBe(true);

    await act(() => {
      (document.activeElement as HTMLElement | null)?.blur();
      state.value = { ...state.value, conn: 'reconnecting' };
    });
    expect(allow().disabled).toBe(true);

    await act(() => {
      state.value = { ...state.value, conn: 'live' };
    });
    expect(allow().disabled).toBe(false);
    expect(document.activeElement).toBe(allow());
  });

  it('re-arms and refocuses the question form when the connection recovers', async () => {
    seed({ question: freeText() });
    vi.spyOn(actions, 'cancelQuestion').mockResolvedValue(null);
    await mount();
    await act(() => {
      key('Escape');
    });
    await flush();
    expect(buttons().every((b) => b.disabled)).toBe(true);

    await act(() => {
      (document.activeElement as HTMLElement | null)?.blur();
      state.value = { ...state.value, conn: 'reconnecting' };
    });
    expect(buttons().every((b) => b.disabled)).toBe(true);

    await act(() => {
      state.value = { ...state.value, conn: 'live' };
    });
    expect(buttons().every((b) => !b.disabled)).toBe(true);
    expect(document.activeElement).toBe(area());
  });
});

describe('Interrupt scroll region', () => {
  it('keeps every permission child inside the inner scroll layer', async () => {
    seed({ permissions: [permission()] });
    await mount();
    const d = dialog()!;
    // The dialog frame only frames the glass; the inner layer scrolls, so the
    // danger glow on the frame can paint outside it.
    const scroll = d.querySelector(':scope > .interrupt__scroll');
    expect(scroll).not.toBeNull();
    expect(Array.from(d.children)).toEqual([scroll]);
    expect(scroll!.querySelector('.interrupt__title')).not.toBeNull();
    expect(scroll!.querySelector('.interrupt__params')).not.toBeNull();
    expect(scroll!.querySelector('.interrupt__actions')).not.toBeNull();
    // Keyboard focus still lands on the first control inside the layer.
    expect(document.activeElement).toBe(allow());
  });

  it('keeps the whole question form inside the inner scroll layer', async () => {
    seed({ question: freeText() });
    await mount();
    const d = dialog()!;
    const scroll = d.querySelector(':scope > .interrupt__scroll');
    expect(scroll).not.toBeNull();
    expect(Array.from(d.children)).toEqual([scroll]);
    // The form itself is the scroll layer, so its controls scroll with it.
    expect(scroll!.tagName).toBe('FORM');
    expect(scroll!.querySelector('.interrupt__actions')).not.toBeNull();
    expect(scroll!.querySelector('textarea')).not.toBeNull();
    expect(document.activeElement).toBe(area());
  });
});

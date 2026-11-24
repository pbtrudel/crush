import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState, pushToast, reduce } from '../state/reducer';
import { state } from '../state/store';
import { effectsLevel } from '../state/effects';
import { Toasts } from './Toasts';
import { signInOpen } from '../state/signin';

const root = document.createElement('div');

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  vi.useRealTimers();
  vi.restoreAllMocks();
  signInOpen.value = null;
});

describe('Toasts', () => {
  it('shows the terminal refusal from the stream', async () => {
    state.value = reduce(initialState(), {
      seq: 1, type: 'web_error', payload: { error: "Can't switch while Crush is busy" },
    });

    await act(() => {
      render(<Toasts />, root);
    });

    expect(root.querySelector('.toast')?.textContent).toContain("Can't switch while Crush is busy");
  });

  it('clears an info toast after 6 s and keeps an error until dismissed', async () => {
    state.value = pushToast(pushToast(initialState(), 'info', 'Saved'), 'error', 'Broke');
    await act(() => {
      render(<Toasts />, root);
    });
    expect(root.querySelectorAll('.toast')).toHaveLength(2);

    await act(() => {
      vi.advanceTimersByTime(5900);
    });
    expect(root.querySelectorAll('.toast')).toHaveLength(2);

    await act(() => {
      vi.advanceTimersByTime(200);
    });
    const left = root.querySelectorAll('.toast');
    expect(left).toHaveLength(1);
    expect(left[0].textContent).toContain('Broke');

    // An error toast outlives any timer; only its own button removes it.
    await act(() => {
      vi.advanceTimersByTime(600_000);
    });
    expect(root.querySelectorAll('.toast')).toHaveLength(1);
    await act(() => {
      root.querySelector<HTMLButtonElement>('.toast__close')!.click();
    });
    expect(root.querySelectorAll('.toast')).toHaveLength(0);
  });

  it('offers a restore button on the reduced-effects toast', async () => {
    effectsLevel.value = 2;
    state.value = pushToast(initialState(), 'warn', 'Reduced effects to keep things smooth', 'restore-effects');
    await act(() => {
      render(<Toasts />, root);
    });
    const restore = Array.from(root.querySelectorAll('button')).find((b) => b.textContent === 'Restore effects');
    expect(restore).toBeDefined();

    await act(() => {
      restore!.click();
    });
    expect(effectsLevel.value).toBe(0);
    expect(state.value.toasts).toEqual([]);
    expect(root.querySelector('.toast')).toBeNull();
  });

  it('offers a sign-in button that names the provider, opens the dialog and dismisses the toast', async () => {
    state.value = pushToast(initialState(), 'warn', 'Your copilot login expired. Sign in again.', {
      kind: 'sign-in', provider: 'copilot',
    });
    await act(() => {
      render(<Toasts />, root);
    });
    const signIn = Array.from(root.querySelectorAll('button')).find((b) => b.textContent === 'Sign in');
    expect(signIn).toBeDefined();
    expect(signIn!.getAttribute('aria-label')).toBe('Sign in to Copilot');
    await act(() => {
      signIn!.click();
    });
    expect(signInOpen.value).toEqual({ provider: 'copilot' });
    expect(state.value.toasts).toEqual([]);
  });

  it('shows no restore button on an ordinary toast', async () => {
    state.value = pushToast(initialState(), 'info', 'Saved');
    await act(() => {
      render(<Toasts />, root);
    });
    expect(Array.from(root.querySelectorAll('button')).some((b) => b.textContent === 'Restore effects')).toBe(false);
  });
});

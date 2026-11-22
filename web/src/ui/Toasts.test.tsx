import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState, reduce } from '../state/reducer';
import { state } from '../state/store';
import { Toasts } from './Toasts';

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
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState } from '../state/reducer';
import { actions, state } from '../state/store';
import { Hud } from './Hud';

const root = document.createElement('div');

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  vi.restoreAllMocks();
});

async function mount() {
  await act(() => {
    render(<Hud />, root);
  });
}

function toggle(name: 'Plan' | 'Code') {
  return Array.from(root.querySelectorAll('button')).find((b) => b.textContent === name)!;
}

describe('Hud input mode', () => {
  it('marks plan mode and asks for code when the toggle is clicked', async () => {
    state.value = { ...initialState(), conn: 'live', inputMode: 'plan' };
    const set = vi.spyOn(actions, 'setInputMode').mockResolvedValue(undefined);
    await mount();

    const group = root.querySelector('[aria-label="Input mode"]')!;
    expect(group.getAttribute('data-mode')).toBe('plan');
    expect(toggle('Plan').getAttribute('aria-pressed')).toBe('true');
    expect(toggle('Code').getAttribute('aria-pressed')).toBe('false');

    await act(() => {
      toggle('Code').click();
    });
    expect(set).toHaveBeenCalledWith('code');
  });

  it('marks code mode by default', async () => {
    state.value = { ...initialState(), conn: 'live' };
    await mount();

    expect(root.querySelector('[aria-label="Input mode"]')!.getAttribute('data-mode')).toBe('code');
    expect(toggle('Code').getAttribute('aria-pressed')).toBe('true');
  });
});

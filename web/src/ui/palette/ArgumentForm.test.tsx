import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { CommandArg } from '../../api/types';
import { ArgumentForm } from './ArgumentForm';

const FILE: CommandArg = { id: 'FILE', title: 'FILE', required: true };
const NOTE: CommandArg = { id: 'NOTE', title: 'Note', required: false };

const root = document.createElement('div');
document.body.append(root);

afterEach(async () => {
  await act(() => { render(null, root); });
  vi.restoreAllMocks();
});

function mount(args: CommandArg[], onSubmit = vi.fn(), onCancel = vi.fn()) {
  return act(() => {
    render(<ArgumentForm title="user:fix" args={args} onSubmit={onSubmit} onCancel={onCancel} />, root);
  });
}

function field(id: string) {
  return root.querySelector<HTMLInputElement>(`#argument-${id}`)!;
}

function submit() {
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

describe('ArgumentForm', () => {
  it('focuses the first field', async () => {
    await mount([FILE, NOTE]);
    expect(document.activeElement).toBe(field('FILE'));
  });

  it('blocks a required field with an inline message', async () => {
    const onSubmit = vi.fn();
    await mount([FILE], onSubmit);
    await act(() => { submit(); });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(root.querySelector('[role="alert"]')!.textContent).toContain('FILE is required');
  });

  it('submits the typed values and leaves optional blanks out', async () => {
    const onSubmit = vi.fn();
    await mount([FILE, NOTE], onSubmit);
    await act(() => {
      const input = field('FILE');
      input.value = 'a.go';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => { submit(); });
    expect(onSubmit).toHaveBeenCalledWith({ FILE: 'a.go' });
  });

  it('cancels back to the list on Escape', async () => {
    const onCancel = vi.fn();
    await mount([FILE], vi.fn(), onCancel);
    await act(() => {
      field('FILE').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true }));
    });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

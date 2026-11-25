import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { useRef } from 'preact/hooks';
import type { PermissionRequest } from '../../api/types';
import { initialState } from '../../state/reducer';
import { state } from '../../state/store';
import { useDialogFocus } from './focus';

const root = document.createElement('div');

function permission(): PermissionRequest {
  return {
    id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
    description: 'Run the tests', action: 'execute', params: {}, path: '/tmp/project',
  };
}

// Harness is the shape every panel has: a layer that goes inert while a
// prompt needs the user, and a dialog inside it that records the control
// holding the focus. hideSecond drops that control, the way a panel drops
// the button an answer replaces.
function Harness({ hideSecond }: { hideSecond: boolean }) {
  const s = state.value;
  const dialog = useRef<HTMLDivElement>(null);
  useDialogFocus(dialog, 'harness', s.permissions.length > 0);
  return (
    <div class="harness-layer" inert={s.permissions.length > 0}>
      <div class="harness" role="dialog" aria-label="Harness" tabIndex={-1} ref={dialog}>
        <button type="button">First</button>
        {!hideSecond && <button type="button">Second</button>}
      </div>
    </div>
  );
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

async function mount() {
  await act(() => { render(<Harness hideSecond={false} />, root); });
  await flush();
}

async function show(hideSecond: boolean) {
  await act(() => { render(<Harness hideSecond={hideSecond} />, root); });
  await flush();
}

async function ask() {
  await act(() => { state.value = { ...state.value, permissions: [permission()] }; });
  await flush();
}

async function answer() {
  await act(() => { state.value = { ...state.value, permissions: [] }; });
  await flush();
}

function button(name: string): HTMLButtonElement {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => b.textContent === name)!;
}

function dialog(): HTMLElement {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Harness"]')!;
}

function layer(): HTMLElement {
  return root.querySelector<HTMLElement>('.harness-layer')!;
}

beforeEach(() => {
  state.value = { ...initialState(), conn: 'live' };
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  state.value = initialState();
});

describe('useDialogFocus', () => {
  it('hands the focus to another control when the focused one unmounts', async () => {
    await mount();
    const second = button('Second');
    second.focus();
    expect(document.activeElement).toBe(second);

    await show(true);

    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(button('First'));
  });

  it('moves no focus on a render while a prompt needs the user', async () => {
    await mount();
    button('First').focus();
    await ask();

    expect(layer().hasAttribute('inert')).toBe(true);
    // jsdom has no inert, so the blur the browser performs is spelled out.
    (document.activeElement as HTMLElement | null)?.blur();
    expect(document.activeElement).toBe(document.body);

    // A prompt that still needs the user owns the focus, so a render of the
    // dialog under it changes nothing.
    await act(() => {
      state.value = { ...state.value, permissions: [permission(), { ...permission(), id: 'p2' }] };
    });
    await flush();

    expect(document.activeElement).toBe(document.body);
  });

  it('gives the remembered control the focus back when the prompt closes', async () => {
    await mount();
    const second = button('Second');
    second.focus();
    await ask();
    (document.activeElement as HTMLElement | null)?.blur();
    expect(document.activeElement).toBe(document.body);

    await answer();

    expect(document.activeElement).toBe(second);
  });

  it('gives the opener the focus back when the dialog closes', async () => {
    const opener = document.createElement('button');
    opener.textContent = 'Open';
    document.body.append(opener);
    try {
      opener.focus();
      await mount();
      expect(dialog().contains(document.activeElement)).toBe(true);

      await act(() => { render(null, root); });
      await flush();

      expect(document.activeElement).toBe(opener);
    } finally {
      opener.remove();
    }
  });
});

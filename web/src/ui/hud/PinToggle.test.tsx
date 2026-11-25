import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Session } from '../../api/types';
import { closeSessions, sessionsOpen } from '../../state/palette';
import { initialState } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { emptyTelemetry, telemetry } from '../../state/telemetry';
import { Hud } from '../Hud';

const root = document.createElement('div');

function session(id: string, title: string): Session {
  return {
    id, parent_session_id: '', title, message_count: 0, prompt_tokens: 0, completion_tokens: 0,
    cost: 0, is_busy: false, created_at: 0, updated_at: 0,
  };
}

async function mount() {
  telemetry.value = emptyTelemetry();
  await act(() => {
    render(<Hud />, root);
  });
}

function button(name: string) {
  return Array.from(root.querySelectorAll('button')).find((b) => b.textContent === name);
}

function labeled(name: string) {
  return root.querySelector<HTMLButtonElement>(`button[aria-label="${name}"]`);
}

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  closeSessions();
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('pin toggle', () => {
  it('stays out of the HUD with one root session', async () => {
    state.value = { ...initialState(), conn: 'live', rootSessions: 1 };
    await mount();
    expect(labeled('Following the terminal')).toBeNull();
    expect(root.textContent).not.toContain('Browsing');
  });

  it('shows the pinned state and opens the session switcher', async () => {
    state.value = { ...initialState(), conn: 'live', rootSessions: 2 };
    await mount();
    const pin = labeled('Following the terminal')!;
    expect(pin).not.toBeNull();
    expect(pin.getAttribute('aria-pressed')).toBe('true');
    await act(() => {
      pin.click();
    });
    expect(sessionsOpen.value).toBe(true);
  });

  it('names the browsed session and returns to the terminal', async () => {
    const repin = vi.spyOn(actions, 'repin').mockResolvedValue(null);
    state.value = {
      ...initialState(),
      conn: 'live',
      rootSessions: 1,
      terminalSession: 's1',
      browsing: 's-2',
      sessionId: 's-2',
      session: session('s-2', 'Tokenizer port'),
    };
    await mount();
    expect(button('Browsing Tokenizer port')).not.toBeUndefined();
    const back = button("Return to the terminal's session")!;
    expect(back).not.toBeUndefined();
    await act(() => {
      back.click();
    });
    expect(repin).toHaveBeenCalled();
  });
});

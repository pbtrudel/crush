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
  return Array.from(root.querySelectorAll('button')).find(
    (b) => accessibleName(b) === name,
  );
}

// accessibleName is the aria-label when one is set, else the visible text;
// the audit requires the visible text to match the name.
function accessibleName(b: HTMLButtonElement) {
  return b.getAttribute('aria-label') ?? b.textContent;
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
    expect(button('Following the terminal')).toBeUndefined();
    expect(root.textContent).not.toContain('Browsing');
  });

  it('shows the pinned state and opens the session switcher', async () => {
    state.value = { ...initialState(), conn: 'live', rootSessions: 2 };
    await mount();
    const pin = button('Following the terminal')!;
    expect(pin).not.toBeUndefined();
    expect(pin.textContent).toBe('Following');
    expect(pin.getAttribute('aria-pressed')).toBe('true');
    expect(accessibleName(pin)).toBe('Following the terminal');
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
    const browse = button('Browsing Tokenizer port')!;
    expect(browse).not.toBeUndefined();
    // The servers column is narrow enough for the pair only with the short
    // visible words; the accessible names keep the full state.
    expect(browse.textContent).toBe('Browsing');
    expect(accessibleName(browse)).toBe('Browsing Tokenizer port');
    const back = button("Return to the terminal's session")!;
    expect(back).not.toBeUndefined();
    expect(back.textContent).toBe('Return');
    expect(accessibleName(back)).toBe("Return to the terminal's session");
    await act(() => {
      back.click();
    });
    expect(repin).toHaveBeenCalled();
  });
});

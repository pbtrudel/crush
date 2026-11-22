import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { initialState } from '../state/reducer';
import { actions, state } from '../state/store';
import { emptyTelemetry, telemetry } from '../state/telemetry';
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

describe('Hud instruments', () => {
  it('shows the gauge, the numerals, the chart and a hex per server', async () => {
    telemetry.value = emptyTelemetry();
    state.value = {
      ...initialState(),
      conn: 'live',
      lsp: {
        gopls: { state: '2', diagnostics: 0, tools: 0 },
        tsserver: { state: '3', diagnostics: 4, tools: 0 },
      },
      mcp: {
        fs: { state: 'connected', diagnostics: 0, tools: 3 },
        gh: { state: 'needs auth', diagnostics: 0, tools: 0 },
      },
    };
    await mount();

    expect(root.querySelector('[role="meter"]')!.getAttribute('aria-label')).toBe('Context');
    expect(Array.from(root.querySelectorAll('.hud-label[aria-hidden="true"]')).map((n) => n.textContent))
      .toEqual(['Tokens', 'Cost', 'Tools 15m']);
    expect(Array.from(root.querySelectorAll('.numeral .visually-hidden')).map((n) => n.textContent))
      .toEqual(['Tokens 0', 'Cost $0.00']);
    expect(root.querySelector('[role="img"][aria-label="Tool calls in the last 15 minutes: 0"]')).not.toBeNull();
    expect(Array.from(root.querySelectorAll('.hex title')).map((t) => t.textContent))
      .toEqual(['gopls: ready', 'tsserver: error, 4 diag', 'fs: ready, 3 tools', 'gh: error']);
  });

  it('publishes its height for the toast offset', async () => {
    state.value = { ...initialState(), conn: 'live' };
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ height: 88 } as DOMRect);
    await mount();

    expect(document.documentElement.style.getPropertyValue('--hud-h')).toBe('88px');
  });
});

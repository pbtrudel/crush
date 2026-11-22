import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame } from '../../api/types';
import { initialState, reduce } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { AgentList } from './AgentList';

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
document.body.append(root);

beforeEach(() => {
  state.value = reduce(initialState(), fx.snapshot);
});
afterEach(async () => {
  await act(() => { render(null, root); });
  vi.restoreAllMocks();
});

const options = () => [...root.querySelectorAll<HTMLElement>('[role="option"]')];
const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));

describe('AgentList', () => {
  it('lists the main agent first and has exactly one tab stop', async () => {
    await act(() => { render(<AgentList />, root); });
    expect(options().map((o) => o.textContent)).toEqual([
      expect.stringContaining('Coder'), expect.stringContaining('Map the parser'), expect.stringContaining('alice'),
    ]);
    expect(options().filter((o) => o.tabIndex === 0)).toHaveLength(1);
  });

  it('moves focus with the arrows, Home and End', async () => {
    await act(() => { render(<AgentList />, root); });
    options()[0].focus();
    await act(() => { key(options()[0], 'ArrowDown'); });
    expect(document.activeElement).toBe(options()[1]);
    expect(options()[1].tabIndex).toBe(0);
    expect(options()[0].tabIndex).toBe(-1);
    await act(() => { key(options()[1], 'End'); });
    expect(document.activeElement).toBe(options()[2]);
    await act(() => { key(options()[2], 'ArrowDown'); });
    // The arrows wrap at both ends.
    expect(document.activeElement).toBe(options()[0]);
    await act(() => { key(options()[0], 'ArrowUp'); });
    expect(document.activeElement).toBe(options()[2]);
  });

  it('selects with Enter, Space and a click', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentList />, root); });
    // The keys act on the roving option, so navigate to it before pressing.
    await act(() => { key(options()[0], 'ArrowDown'); });
    await act(() => { key(options()[1], 'Enter'); });
    expect(inspect).toHaveBeenLastCalledWith('sub-1');
    await act(() => { key(options()[1], 'ArrowDown'); });
    await act(() => { key(options()[2], ' '); });
    expect(inspect).toHaveBeenLastCalledWith('team-m-1');
    await act(() => { options()[0].click(); });
    expect(inspect).toHaveBeenLastCalledWith('s1');
  });

  it('marks the inspected agent selected and keeps the tab stop on it after the roster changes', async () => {
    vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    state.value = { ...state.value, inspected: { sessionId: 'team-m-1', session: null, messages: [], loading: false } };
    await act(() => { render(<AgentList />, root); });
    expect(options()[2].getAttribute('aria-selected')).toBe('true');
    await act(() => { options()[2].click(); });
    await act(() => { state.value = { ...state.value, runs: state.value.runs.filter((r) => r.session_id !== 'sub-1') }; });
    expect(options().find((o) => o.tabIndex === 0)?.textContent).toContain('alice');
  });

  it('says the status in text', async () => {
    await act(() => { render(<AgentList />, root); });
    expect(options()[1].textContent).toContain('Running');
  });
});

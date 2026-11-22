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
// press keeps the event so a test can tell whether the list cancelled it.
const press = (el: Element, k: string, mods: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...mods });
  el.dispatchEvent(e);
  return e;
};

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

  it('moves the tab stop to an agent inspected outside the list', async () => {
    await act(() => { render(<AgentList />, root); });
    // A click inside the list owns the tab stop first.
    await act(() => { options()[0].click(); });
    // A node click selects an agent the keyboard never visited.
    await act(() => {
      state.value = { ...state.value, inspected: { sessionId: 'team-m-1', session: null, messages: [], loading: false } };
    });
    expect(options()[2].tabIndex).toBe(0);
    expect(options()[0].tabIndex).toBe(-1);
  });

  it('selects the option the key event came from', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentList />, root); });
    // The roving tab stop is still on the main agent, so the event target
    // decides which option the key selects, child element or not.
    await act(() => { press(options()[2].querySelector('.agent-list__name')!, 'Enter'); });
    expect(inspect).toHaveBeenLastCalledWith('team-m-1');
    await act(() => { press(options()[1], ' '); });
    expect(inspect).toHaveBeenLastCalledWith('sub-1');
  });

  it('leaves Alt, Ctrl and Meta chords to the browser', async () => {
    await act(() => { render(<AgentList />, root); });
    options()[0].focus();
    const alt = press(options()[0], 'ArrowLeft', { altKey: true });
    expect(document.activeElement).toBe(options()[0]);
    expect(alt.defaultPrevented).toBe(false);
    const ctrl = press(options()[0], 'ArrowDown', { ctrlKey: true });
    expect(document.activeElement).toBe(options()[0]);
    expect(ctrl.defaultPrevented).toBe(false);
    // A plain arrow still moves and still suppresses the page scroll.
    const plain = press(options()[0], 'ArrowDown');
    expect(document.activeElement).toBe(options()[1]);
    expect(plain.defaultPrevented).toBe(true);
  });

  it('keeps one tab stop when the focused agent leaves the roster', async () => {
    await act(() => { render(<AgentList />, root); });
    options()[0].focus();
    await act(() => { key(options()[0], 'ArrowDown'); });
    expect(document.activeElement).toBe(options()[1]);
    // Removing the focused option must not throw or leave two tab stops.
    await act(() => {
      state.value = { ...state.value, runs: state.value.runs.filter((r) => r.session_id !== 'sub-1') };
    });
    expect(options().map((o) => o.textContent)).toEqual([
      expect.stringContaining('Coder'), expect.stringContaining('alice'),
    ]);
    expect(options().filter((o) => o.tabIndex === 0)).toHaveLength(1);
    expect(options()[0].tabIndex).toBe(0);
  });
});

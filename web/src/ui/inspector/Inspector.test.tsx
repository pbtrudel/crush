import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame, Message, WireEvent } from '../../api/types';
import { initialState, inspect, reduce } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { Inspector } from './Inspector';

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
document.body.append(root);
const child = (fx.message_child.payload as WireEvent<Message>).payload;

function open(sid: string, messages: Message[] = []) {
  const s = inspect(reduce(initialState(), fx.snapshot), sid);
  state.value = { ...s, inspected: { ...s.inspected!, messages, loading: false } };
}

beforeEach(() => {
  vi.useFakeTimers({ now: (child.updated_at + 42) * 1000 });
});
afterEach(async () => {
  await act(() => { render(null, root); });
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const region = () => root.querySelector<HTMLElement>('section.panel--inspector')!;

describe('Inspector', () => {
  it('is hidden without a selection', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<Inspector />, root); });
    expect(region().hidden).toBe(true);
  });

  it('shows what a subagent is doing and cancels it', async () => {
    open('sub-1', [child]);
    const cancel = vi.spyOn(actions, 'cancelAgent').mockResolvedValue(null);
    await act(() => { render(<Inspector />, root); });
    expect(region().hidden).toBe(false);
    expect(root.querySelector('#inspector-title')!.textContent).toBe('Map the parser');
    expect(region().textContent).toContain('Subagent');
    expect(region().textContent).toContain('Running');
    expect(region().querySelector('[data-fact="tool"]')!.textContent).toContain('grep');
    expect(region().querySelector('[data-fact="tool"]')!.textContent).toContain('42s');
    expect(region().querySelector('[role="log"]')!.textContent).toContain('Scanning the tokenizer.');
    await act(() => { root.querySelector<HTMLButtonElement>('button[data-action="cancel"]')!.click(); });
    expect(cancel).toHaveBeenCalledWith('sub-1');
  });

  it('shows a teammate task and model, interrupts it and sends a message', async () => {
    open('team-m-1');
    const interrupt = vi.spyOn(actions, 'interruptTeammate').mockResolvedValue(null);
    const send = vi.spyOn(actions, 'messageTeammate').mockResolvedValue(null);
    await act(() => { render(<Inspector />, root); });
    expect(region().querySelector('[data-fact="task"]')!.textContent).toContain('Port the parser');
    expect(region().querySelector('[data-fact="model"]')!.textContent).toContain('claude');
    await act(async () => { root.querySelector<HTMLButtonElement>('button[data-action="interrupt"]')!.click(); });
    expect(interrupt).toHaveBeenCalledWith('alice');
    const box = root.querySelector<HTMLTextAreaElement>('textarea')!;
    await act(() => {
      box.value = 'rebase first';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { root.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
    // The notice is painted a microtask after the mocked action resolves,
    // which is after the act() that dispatched the submit has flushed.
    await act(async () => {});
    expect(send).toHaveBeenCalledWith('alice', 'rebase first');
    expect(box.value).toBe('');
    expect(region().querySelector('[role="status"]')!.textContent).toContain('Sent to alice');
  });

  it('shows a refused action inline', async () => {
    open('team-m-1');
    vi.spyOn(actions, 'interruptTeammate').mockResolvedValue('Agent Teams is not enabled');
    await act(() => { render(<Inspector />, root); });
    await act(async () => { root.querySelector<HTMLButtonElement>('button[data-action="interrupt"]')!.click(); });
    expect(region().querySelector('[role="alert"]')!.textContent).toContain('Agent Teams is not enabled');
  });

  it('keeps the inspector open when the agent leaves the roster', async () => {
    open('sub-1', [child]);
    await act(() => { render(<Inspector />, root); });
    await act(() => { state.value = { ...state.value, runs: state.value.runs.filter((r) => r.session_id !== 'sub-1') }; });
    expect(region().hidden).toBe(false);
    expect(region().textContent).toContain('Done');
    expect(root.querySelector('button[data-action="cancel"]')).toBeNull();
  });

  it('says the agent is gone when its session was pruned before the inspector saw it', async () => {
    const s = reduce(initialState(), fx.snapshot);
    state.value = {
      ...s,
      runs: s.runs.filter((r) => r.session_id !== 'sub-1'),
      inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false },
    };
    await act(() => { render(<Inspector />, root); });
    expect(region().hidden).toBe(false);
    expect(root.querySelector('#inspector-title')!.textContent).toBe('Agent gone');
    expect(region().textContent).toContain('no longer in the roster');
    expect(root.querySelector('button[data-action]')).toBeNull();
  });

  it('closes on Escape and gives focus back to the agent option', async () => {
    open('sub-1', [child]);
    const option = document.createElement('li');
    option.id = 'agent-sub-1';
    option.tabIndex = -1;
    document.body.append(option);
    const close = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<Inspector />, root); });
    root.querySelector<HTMLElement>('#inspector-title')!.focus();
    await act(() => { region().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); });
    expect(close).toHaveBeenCalledWith(null);
    expect(document.activeElement).toBe(option);
    option.remove();
  });

  it('shows the main transcript for the main agent, with no extra actions', async () => {
    const s = reduce(reduce(initialState(), fx.snapshot), fx.message_user);
    state.value = inspect(s, 's1');
    await act(() => { render(<Inspector />, root); });
    expect(root.querySelector('#inspector-title')!.textContent).toBe('Coder');
    expect(region().querySelector('[role="log"]')!.textContent).toContain('Refactor the parser');
    expect(root.querySelector('button[data-action]')).toBeNull();
  });
});

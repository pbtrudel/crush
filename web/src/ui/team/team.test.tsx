import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame } from '../../api/types';
import { initialState, reduce } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { TaskBoard } from './TaskBoard';
import { TodoOrbit } from './TodoOrbit';

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
afterEach(async () => {
  await act(() => { render(null, root); });
  vi.restoreAllMocks();
});

describe('TaskBoard', () => {
  it('sorts the scoped tasks into three lanes', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<TaskBoard />, root); });
    const lane = (name: string) => [...root.querySelectorAll<HTMLElement>('.taskboard__lane')].find((l) => l.querySelector('h3')!.textContent!.startsWith(name))!;
    expect(lane('In progress').textContent).toContain('Port the parser');
    expect(lane('Pending').textContent).toContain('Write parser tests');
    expect(lane('Pending').textContent).toContain('Blocked by task-1');
    expect(lane('Completed').querySelectorAll('li')).toHaveLength(0);
  });

  it('links an owner chip to its agent', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<TaskBoard />, root); });
    await act(() => { root.querySelector<HTMLButtonElement>('.taskcard__owner')!.click(); });
    expect(inspect).toHaveBeenCalledWith('team-m-1');
  });

  it('renders nothing without team tasks', async () => {
    state.value = initialState();
    await act(() => { render(<TaskBoard />, root); });
    expect(root.innerHTML).toBe('');
  });
});

describe('TodoOrbit', () => {
  it('lists todos with their status in text and counts the done ones', async () => {
    state.value = reduce(reduce(initialState(), fx.snapshot), fx.session);
    await act(() => { render(<TodoOrbit />, root); });
    expect(root.querySelector('h2')!.textContent).toContain('1/3');
    const items = [...root.querySelectorAll('li')].map((li) => li.textContent);
    expect(items[0]).toContain('Map the parser');
    expect(items[0]).toContain('done');
    expect(items[1]).toContain('Porting the parser');
    expect(items[1]).toContain('in progress');
  });

  it('renders nothing without todos', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<TodoOrbit />, root); });
    expect(root.innerHTML).toBe('');
  });
});

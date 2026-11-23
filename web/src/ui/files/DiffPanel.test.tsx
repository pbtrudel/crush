import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import { ApiError, api } from '../../api/http';
import type { FileDiff, Frame } from '../../api/types';
import { diffMode } from '../../state/drawer';
import { initialState, reduce } from '../../state/reducer';
import { actions, state } from '../../state/store';

const mounted = vi.hoisted(() => ({ modes: [] as string[], destroyed: 0 }));
vi.mock('./diff/codemirror', () => ({
  mountDiff: async (_host: HTMLElement, _d: unknown, mode: string) => {
    mounted.modes.push(mode);
    return () => { mounted.destroyed++; };
  },
}));
import { DiffPanel } from './DiffPanel';

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const diff = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: '/repo/parser.go', from: 0, to: 1, old: 'a\n', new: 'a\nb\n',
  unified: '--- a/repo/parser.go\n+++ b/repo/parser.go\n@@ -1 +1,2 @@\n a\n+b\n', language: 'go', truncated: false, ...over,
});
const flush = async () => { for (let i = 0; i < 2; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); }); };

beforeEach(() => {
  state.value = reduce(initialState(), fx.snapshot);
  mounted.modes = [];
  mounted.destroyed = 0;
  diffMode.value = 'split';
});
afterEach(async () => {
  await act(() => { render(null, root); });
  vi.restoreAllMocks();
});

describe('DiffPanel', () => {
  it('loads the diff and mounts the editor lazily in the chosen layout', async () => {
    const get = vi.spyOn(api, 'diff').mockResolvedValue(diff());
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    expect(get).toHaveBeenCalledWith('s1', '/repo/parser.go', undefined, undefined);
    expect(mounted.modes).toEqual(['split']);
    await act(() => { root.querySelector<HTMLButtonElement>('button[data-mode="unified"]')!.click(); });
    await flush();
    expect(mounted.modes).toEqual(['split', 'unified']);
    expect(mounted.destroyed).toBe(1);
  });

  it('renders the unified fallback when the diff is truncated', async () => {
    vi.spyOn(api, 'diff').mockResolvedValue(diff({ old: '', new: '', truncated: true }));
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    expect(mounted.modes).toEqual([]);
    expect(root.textContent).toContain('over 1 MB');
    expect(root.querySelector('.diff-unified [data-kind="add"]')!.textContent).toContain('+b');
    expect(root.querySelector('.diff-unified [data-kind="hunk"]')).not.toBeNull();
  });

  // A truncated answer can carry no text at all: the notice still has to
  // show, without an empty box below it.
  it('shows the truncated notice without an empty diff body', async () => {
    vi.spyOn(api, 'diff').mockResolvedValue(diff({ old: '', new: '', unified: '', truncated: true }));
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    expect(mounted.modes).toEqual([]);
    expect(root.textContent).toContain('over 1 MB');
    expect(root.textContent).toContain('Diff text not available');
    expect(root.querySelector('pre.diff-unified')).toBeNull();
  });

  it('asks for the versions the stepper picks', async () => {
    const get = vi.spyOn(api, 'diff').mockResolvedValue(diff());
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    const from = root.querySelector<HTMLSelectElement>('select[name="from"]')!;
    expect([...from.options].map((o) => o.value)).toEqual(['0', '1']);
    await act(() => {
      from.value = '1';
      from.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await flush();
    expect(get).toHaveBeenLastCalledWith('s1', '/repo/parser.go', 1, 1);
  });

  // version_ids are positions in the tree's history, so a file with three
  // versions offers 0, 1 and 2 even when the history records carry other
  // numbers (the seeded parser.go starts at version 0 twice, in two
  // sessions).
  it('offers every version position the gateway lists', async () => {
    state.value = {
      ...reduce(initialState(), fx.snapshot),
      files: [{
        path: '/repo/parser.go', versions: 3, version_ids: [0, 1, 2], additions: 3, removals: 1,
        diagnostics: { error: 1, warning: 0 }, updated_at: 104, session_ids: ['s1', 'sub-1'],
      }],
    };
    const get = vi.spyOn(api, 'diff').mockResolvedValue(diff({ from: 0, to: 2 }));
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    const from = root.querySelector<HTMLSelectElement>('select[name="from"]')!;
    const to = root.querySelector<HTMLSelectElement>('select[name="to"]')!;
    expect([...from.options].map((o) => o.value)).toEqual(['0', '1', '2']);
    expect([...to.options].map((o) => o.value)).toEqual(['0', '1', '2']);
    expect(from.value).toBe('0');
    expect(to.value).toBe('2');
    await act(() => {
      from.value = '1';
      from.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await flush();
    expect(get).toHaveBeenLastCalledWith('s1', '/repo/parser.go', 1, 2);
  });

  it('shows the gateway error with a retry', async () => {
    const get = vi.spyOn(api, 'diff').mockRejectedValue(new ApiError(404, 'unknown_path', 'the session has no history for this path'));
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    expect(root.querySelector('[role="alert"]')!.textContent).toContain('the session has no history for this path');
    await act(() => { root.querySelector<HTMLButtonElement>('[role="alert"] button')!.click(); });
    await flush();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it('links the agents that edited the file', async () => {
    vi.spyOn(api, 'diff').mockResolvedValue(diff());
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<DiffPanel path="/repo/parser.go" />, root); });
    await flush();
    const chips = [...root.querySelectorAll<HTMLButtonElement>('.diffpanel__editors button')];
    expect(chips.map((c) => c.textContent)).toEqual(['Coder', 'Map the parser']);
    // The chip names the agent it inspects, and leaves the agent ids to the
    // roster and the scene.
    expect(chips.map((c) => c.getAttribute('aria-label'))).toEqual(['Inspect Coder', 'Inspect Map the parser']);
    expect(root.querySelector('[data-agent-node], [id^="agent-"]')).toBeNull();
    await act(() => { chips[1].click(); });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });
});

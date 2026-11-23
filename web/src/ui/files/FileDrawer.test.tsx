import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame } from '../../api/types';
import { drawerOpen, panelTab, selectedFile } from '../../state/drawer';
import { initialState, reduce } from '../../state/reducer';
import { state } from '../../state/store';
import { FileDrawer } from './FileDrawer';

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
beforeEach(() => {
  drawerOpen.value = true;
  selectedFile.value = null;
});
afterEach(async () => {
  await act(() => { render(null, root); });
});

describe('FileDrawer', () => {
  it('explains the empty state', async () => {
    state.value = { ...reduce(initialState(), fx.snapshot), files: [] };
    await act(() => { render(<FileDrawer />, root); });
    expect(root.textContent).toContain('No edited files yet. Edits appear here as agents write.');
  });

  it('lists a file with its change counts and a spoken diagnostic count', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<FileDrawer />, root); });
    const row = root.querySelector<HTMLButtonElement>('button.file-row')!;
    expect(row.textContent).toContain('parser.go');
    expect(row.textContent).toContain('+3');
    expect(row.textContent).toContain('−1');
    expect(row.textContent).toContain('1 error');
    expect(root.querySelectorAll('.file-row__bar')).toHaveLength(5);
  });

  // A file whose diff was too large to count reports unknown, not zero.
  it('shows unknown stats as unknown rather than zero', async () => {
    state.value = {
      ...reduce(initialState(), fx.snapshot),
      files: [{
        path: '/repo/big.go', versions: 2, version_ids: [0, 1], additions: 0, removals: 0,
        diagnostics: { error: 0, warning: 0 }, updated_at: 1, session_ids: ['s1'], stats_unknown: true,
      }],
    };
    await act(() => { render(<FileDrawer />, root); });
    const row = root.querySelector<HTMLButtonElement>('button.file-row')!;
    expect(row.textContent).toContain('unknown');
    expect(row.textContent).not.toContain('+0');
    expect(row.textContent).not.toContain('−0');
  });

  it('selects a file', async () => {
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<FileDrawer />, root); });
    await act(() => { root.querySelector<HTMLButtonElement>('button.file-row')!.click(); });
    expect(selectedFile.value).toBe('/repo/parser.go');
    expect(root.querySelector('button.file-row')!.getAttribute('aria-pressed')).toBe('true');
  });

  it('is inert while closed', async () => {
    drawerOpen.value = false;
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<FileDrawer />, root); });
    expect(root.querySelector('aside')!.hasAttribute('inert')).toBe(true);
  });

  // Below 1024px the closed drawer leaves the shared slot, so the log has to
  // come back instead of an empty panel.
  it('gives the narrow panel slot back to the log on close', async () => {
    panelTab.value = 'files';
    state.value = reduce(initialState(), fx.snapshot);
    await act(() => { render(<FileDrawer />, root); });
    await act(() => { root.querySelector<HTMLButtonElement>('.drawer__head .btn')!.click(); });
    expect(drawerOpen.value).toBe(false);
    expect(panelTab.value).toBe('log');
  });
});

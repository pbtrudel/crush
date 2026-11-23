import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import { ApiError, api } from '../../api/http';
import type { Frame } from '../../api/types';
import { drawerOpen, selectedFile } from '../../state/drawer';
import { adoptSession, initialState, reduce } from '../../state/reducer';
import { state } from '../../state/store';
import { FileDrawer } from './FileDrawer';

// This spec mounts the real DiffPanel, so it pins what the drawer shows
// after a session switch: no panel, no stale error and no Retry.
const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const flush = async () => { for (let i = 0; i < 2; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); }); };

beforeEach(() => {
  drawerOpen.value = true;
  selectedFile.value = null;
  state.value = reduce(initialState(), fx.snapshot);
});
afterEach(async () => {
  await act(() => { render(null, root); });
  state.value = initialState();
  selectedFile.value = null;
  vi.restoreAllMocks();
});

describe('FileDrawer after a session switch', () => {
  it('drops the previous session\'s diff and its retry', async () => {
    vi.spyOn(api, 'diff').mockRejectedValue(new ApiError(404, 'unknown_path', 'the session has no history for this path'));
    await act(() => { render(<FileDrawer />, root); });
    await act(() => { root.querySelector<HTMLButtonElement>('button.file-row')!.click(); });
    await flush();
    expect(root.querySelector('.diffpanel')).not.toBeNull();
    expect(root.querySelector('[role="alert"]')!.textContent).toContain('no history');

    // The terminal switches to another session, which has no files yet.
    await act(() => { state.value = adoptSession(state.value, 's2'); });
    await flush();
    expect(selectedFile.value).toBeNull();
    expect(root.querySelector('.diffpanel')).toBeNull();
    expect(root.querySelector('[role="alert"]')).toBeNull();
    expect(root.textContent).not.toContain('Retry');
  });
});

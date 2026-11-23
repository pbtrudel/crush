import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../state/__fixtures__/events.json';
import type { Frame } from '../api/types';
import { drawerOpen, panelTab } from '../state/drawer';
import { initialState, reduce } from '../state/reducer';
import { state } from '../state/store';
import { App } from './App';

vi.mock('../scene/mount', () => ({ mountScene: () => { throw new Error('no webgl'); } }));

const fx = fixtures as unknown as Record<string, Frame>;

describe('App layout', () => {
  it('puts reading surfaces on glass and glanceable ones on hologram, in the same DOM order', async () => {
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    await act(() => { render(<App />, root); });
    expect(root.querySelector('.hud')!.classList.contains('holo')).toBe(true);
    expect(root.querySelector('.panel--log')!.classList.contains('glass')).toBe(true);
    expect(root.querySelector('.holodeck__deck .deck')!.classList.contains('glass')).toBe(true);
    const order = Array.from(root.querySelectorAll('.hud, .panel--log, .stage, .holodeck__deck')).map((e) => e.className.split(' ')[0]);
    expect(order).toEqual(['hud', 'panel', 'stage', 'holodeck__deck']);
    render(null, root);
  });

  it('places the drawer after the stage and records the open panel on the grid', async () => {
    drawerOpen.value = false;
    panelTab.value = 'log';
    state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live' };
    const root = document.createElement('div');
    try {
      await act(() => { render(<App />, root); });
      const grid = root.querySelector<HTMLElement>('.holodeck__grid')!;
      expect(grid.getAttribute('data-drawer')).toBe('closed');
      expect(grid.getAttribute('data-panel-tab')).toBe('log');
      expect(root.querySelector('.panel-tabs')!.getAttribute('role')).toBe('group');
      const stage = root.querySelector('.stage')!;
      const drawer = root.querySelector('#file-drawer')!;
      expect([...grid.children].indexOf(drawer)).toBe([...grid.children].indexOf(stage) + 1);
      await act(() => { root.querySelector<HTMLButtonElement>('.drawer-handle')!.click(); });
      expect(grid.getAttribute('data-drawer')).toBe('open');
      expect(grid.getAttribute('data-panel-tab')).toBe('files');
      expect(drawer.hasAttribute('inert')).toBe(false);
      const tabs = [...root.querySelectorAll<HTMLButtonElement>('.panel-tabs button')];
      await act(() => { tabs.find((b) => b.textContent === 'Log')!.click(); });
      expect(grid.getAttribute('data-drawer')).toBe('closed');
      expect(grid.getAttribute('data-panel-tab')).toBe('log');
      await act(() => { tabs.find((b) => b.textContent === 'Files')!.click(); });
      expect(grid.getAttribute('data-drawer')).toBe('open');
      expect(grid.getAttribute('data-panel-tab')).toBe('files');
    } finally {
      await act(() => { render(null, root); });
      drawerOpen.value = false;
      panelTab.value = 'log';
      state.value = initialState();
    }
  });

  it('makes the panel tabs inert with the rest of the page while a dialog is open', async () => {
    const permission = {
      id: 'p1', session_id: 's1', tool_call_id: 'tc1', tool_name: 'bash',
      description: 'Run the tests', action: 'execute', params: {}, path: '/tmp/project',
    };
    state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live' };
    const root = document.createElement('div');
    try {
      await act(() => { render(<App />, root); });
      const tabs = root.querySelector('.panel-tabs')!;
      const main = root.querySelector('main')!;
      expect(tabs.hasAttribute('inert')).toBe(false);
      await act(() => { state.value = { ...state.value, permissions: [permission] }; });
      expect(main.hasAttribute('inert')).toBe(true);
      expect(tabs.hasAttribute('inert')).toBe(true);
      await act(() => { state.value = { ...state.value, permissions: [] }; });
      expect(tabs.hasAttribute('inert')).toBe(false);
    } finally {
      await act(() => { render(null, root); });
      state.value = initialState();
    }
  });
});

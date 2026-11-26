import { describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../state/__fixtures__/events.json';
import type { Frame, SecurePrompt } from '../api/types';
import { drawerOpen, panelTab } from '../state/drawer';
import { overlayWidth } from '../state/overlay';
import { adoptSession, initialState, reduce } from '../state/reducer';
import { dispatch, state } from '../state/store';
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

  it('inerts the shell under a secure prompt and keeps the focus in its dialog', async () => {
    const prompt: SecurePrompt = {
      source: 'pinentry', id: 'p1', kind: 'passphrase', title: 'GPG passphrase',
      message: 'Unlock the signing key',
    };
    state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live' };
    const root = document.createElement('div');
    // The focus check needs the tree in the document: jsdom ignores focus on
    // a detached element.
    document.body.append(root);
    try {
      await act(() => { render(<App />, root); });
      const main = root.querySelector('main')!;
      const deck = root.querySelector('.holodeck__deck')!;
      expect(main.hasAttribute('inert')).toBe(false);
      await act(() => { state.value = { ...state.value, securePrompts: [prompt] }; });
      expect(main.hasAttribute('inert')).toBe(true);
      expect(deck.hasAttribute('inert')).toBe(true);
      const dialog = root.querySelector<HTMLElement>('[role="dialog"]')!;
      expect(dialog.contains(document.activeElement)).toBe(true);
      await act(() => { state.value = { ...state.value, securePrompts: [] }; });
      expect(main.hasAttribute('inert')).toBe(false);
      expect(deck.hasAttribute('inert')).toBe(false);
    } finally {
      await act(() => { render(null, root); });
      root.remove();
      state.value = initialState();
    }
  });

  it('publishes the whole deck column for the AWS card offset', async () => {
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    // The column holds the side answer and the deck, so it is taller than the
    // form the AWS card used to be placed above. Its top is measured too: on
    // a short screen the deck runs past the shell, so the card cannot derive
    // the top from the viewport and the height.
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const deck = this.classList.contains('holodeck__deck');
      const height = deck ? 240 : 146;
      const top = deck ? 320 : 0;
      return { height, top } as DOMRect;
    });
    try {
      await act(() => { render(<App />, root); });
      expect(document.documentElement.style.getPropertyValue('--deck-h')).toBe('240px');
      expect(document.documentElement.style.getPropertyValue('--deck-top')).toBe('320px');
    } finally {
      await act(() => { render(null, root); });
      expect(document.documentElement.style.getPropertyValue('--deck-h')).toBe('');
      expect(document.documentElement.style.getPropertyValue('--deck-top')).toBe('');
      vi.restoreAllMocks();
      state.value = initialState();
    }
  });
});

// Below 1024px the agent tab is the inspector's slot. A session switch
// clears the inspector, so the slot must fall back to the log instead of
// going blank with the agent tab still pressed.
describe('the narrow panel slot', () => {
  it('shows the log when the agent tab has no inspector to show', async () => {
    drawerOpen.value = false;
    panelTab.value = 'agent';
    state.value = {
      ...reduce(initialState(), fx.snapshot),
      conn: 'live',
      inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false },
    };
    const root = document.createElement('div');
    const tabs = () => [...root.querySelectorAll<HTMLButtonElement>('.panel-tabs button')];
    const pressed = (label: string) => tabs().find((b) => b.textContent === label)!.getAttribute('aria-pressed');
    try {
      await act(() => { render(<App />, root); });
      const grid = root.querySelector<HTMLElement>('.holodeck__grid')!;
      expect(grid.getAttribute('data-panel-tab')).toBe('agent');
      expect(pressed('Agent')).toBe('true');

      // The terminal switches session, which drops the inspection.
      await act(() => { dispatch((s) => adoptSession(s, 's2')); });
      expect(state.value.inspected).toBeNull();
      expect(grid.getAttribute('data-panel-tab')).toBe('log');
      expect(pressed('Log')).toBe('true');
      expect(pressed('Agent')).toBe('false');
    } finally {
      await act(() => { render(null, root); });
      drawerOpen.value = false;
      panelTab.value = 'log';
      state.value = initialState();
    }
  });
});

// One live region per state: two polite regions saying the same thing make
// a screen reader repeat every connection change.
describe('connection status', () => {
  const regions = (root: Element) => [...root.querySelectorAll('[role="status"]')]
    .filter((el) => /Linked|Reconnecting|Signal lost|Session ended/.test(el.textContent ?? ''));

  it('announces a lost or ended link from the HUD only', async () => {
    state.value = { ...initialState(), conn: 'reconnecting' };
    const root = document.createElement('div');
    try {
      await act(() => { render(<App />, root); });
      const live = regions(root);
      expect(live).toHaveLength(1);
      expect(live[0].classList.contains('hud__link')).toBe(true);
      expect(live[0].textContent).toContain('Signal lost. Reconnecting to crush.');

      // The banner still shows the instruction; it just has nothing to say.
      const banner = root.querySelector('.banner')!;
      expect(banner.getAttribute('aria-hidden')).toBe('true');
      expect(banner.hasAttribute('role')).toBe(false);
      expect(banner.textContent).toContain('Signal lost');

      await act(() => { state.value = { ...state.value, conn: 'ended' }; });
      expect(regions(root)).toHaveLength(1);
      expect(regions(root)[0].textContent).toContain('Start crush with --web');
      expect(root.querySelector('.banner')!.getAttribute('aria-hidden')).toBe('true');
    } finally {
      await act(() => { render(null, root); });
      state.value = initialState();
    }
  });

  it('names a browser detach instead of a session end', async () => {
    state.value = { ...initialState(), conn: 'ended', endedReason: 'detached' };
    const root = document.createElement('div');
    const stopped = 'Web mode was stopped. Run /web in the terminal for a new link.';
    try {
      await act(() => { render(<App />, root); });
      expect(root.querySelector('.hud__link')!.textContent).toContain(stopped);
      expect(root.querySelector('.banner')!.textContent).toContain(stopped);
      expect(root.querySelector('.deck__help')!.textContent).toContain(stopped);
      const deck = root.querySelector<HTMLTextAreaElement>('#deck-input')!;
      expect(deck.disabled).toBe(true);
      expect(deck.placeholder).toBe('Web mode stopped');
    } finally {
      await act(() => { render(null, root); });
      state.value = initialState();
    }
  });
});

// Between 1024px and 1279px the open drawer covers the log, so the log it
// covers leaves the tab order. Every other layout keeps it reachable.
describe('the covered log', () => {
  it('is inert only over the overlay drawer', async () => {
    drawerOpen.value = false;
    overlayWidth.value = false;
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    try {
      await act(() => { render(<App />, root); });
      const log = root.querySelector('.panel--log')!;
      expect(log.hasAttribute('inert')).toBe(false);
      await act(() => { drawerOpen.value = true; });
      expect(log.hasAttribute('inert')).toBe(false);
      await act(() => { overlayWidth.value = true; });
      expect(log.hasAttribute('inert')).toBe(true);
      await act(() => { overlayWidth.value = false; });
      expect(log.hasAttribute('inert')).toBe(false);
      await act(() => { drawerOpen.value = false; });
      expect(log.hasAttribute('inert')).toBe(false);
    } finally {
      await act(() => { render(null, root); });
      drawerOpen.value = false;
      overlayWidth.value = false;
      state.value = initialState();
    }
  });

  it('hands the focus to the drawer that covers it, and back to the handle', async () => {
    drawerOpen.value = false;
    overlayWidth.value = false;
    state.value = { ...initialState(), conn: 'live' };
    const root = document.createElement('div');
    // Focus needs a connected tree.
    document.body.append(root);
    try {
      await act(() => { render(<App />, root); });
      const log = root.querySelector<HTMLElement>('.panel--log')!;
      // The transcript may be empty at this point, so focus a stand-in: the
      // handoff only cares about where the focus is.
      const probe = document.createElement('button');
      log.append(probe);
      probe.focus();
      expect(document.activeElement).toBe(probe);
      const close = root.querySelector<HTMLElement>('#file-drawer .drawer__head button')!;
      const handle = root.querySelector<HTMLElement>('.drawer-handle')!;
      await act(() => { drawerOpen.value = true; overlayWidth.value = true; });
      expect(document.activeElement, 'the drawer takes the focus off the inert log').toBe(close);
      await act(() => { drawerOpen.value = false; });
      expect(document.activeElement, 'the handle gets the focus back').toBe(handle);
    } finally {
      await act(() => { render(null, root); });
      root.remove();
      drawerOpen.value = false;
      overlayWidth.value = false;
      state.value = initialState();
    }
  });
});

describe('the pending plan and the tab title', () => {
  it('marks the title and the core as needing you without a modal', async () => {
    state.value = {
      ...reduce(initialState(), fx.snapshot),
      conn: 'live',
      sessionId: 's1',
      planReady: 's1',
      inputMode: 'plan',
    };
    const root = document.createElement('div');
    const before = document.title;
    try {
      await act(() => { render(<App />, root); });
      expect(document.title.startsWith('(!) ')).toBe(true);
      // The log stays interactive: the plan card is not the permission modal.
      expect(root.querySelector('main')!.hasAttribute('inert')).toBe(false);
      expect(root.querySelector('.panel--log')!.hasAttribute('inert')).toBe(false);

      await act(() => { state.value = { ...state.value, planReady: '' }; });
      expect(document.title.startsWith('(!) ')).toBe(false);
    } finally {
      await act(() => { render(null, root); });
      state.value = initialState();
      document.title = before;
    }
  });
});

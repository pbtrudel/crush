import { afterEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame } from '../api/types';
import { drawerOpen, panelTab } from './drawer';
import { focusToolCall, toolFocus } from './focusTool';
import { logInert, overlayWidth } from './overlay';
import { initialState, reduce } from './reducer';
import { actions, state } from './store';

const fx = fixtures as unknown as Record<string, Frame>;

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  toolFocus.value = null;
  state.value = initialState();
  drawerOpen.value = false;
  panelTab.value = 'log';
  overlayWidth.value = false;
});

// viewport makes the panel-slot media query answer the way the named width
// band does. With no stub the query is absent, which is the wide layout.
function viewport(narrow: boolean) {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: narrow })));
}

describe('focusToolCall', () => {
  it('sets the focus request for a call in the shown session', () => {
    state.value = reduce(initialState(), fx.snapshot);
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('s1', 'tc-1');

    expect(toolFocus.value).toEqual({ callId: 'tc-1', sessionId: 's1' });
    // The main agent is the selection when no child agent is inspected.
    expect(inspect).toHaveBeenCalledWith(null);
  });

  it('selects the main agent again when a child inspector is open', () => {
    state.value = {
      ...reduce(initialState(), fx.snapshot),
      inspected: { sessionId: 'sub-1', session: null, messages: [], loading: true },
    };
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('s1', 'tc-1');

    expect(inspect).toHaveBeenCalledWith(null);
  });

  it('opens the inspector for the child session that ran the call', () => {
    state.value = reduce(initialState(), fx.snapshot);
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('sub-1', 'sub-tc-1');

    expect(toolFocus.value).toEqual({ callId: 'sub-tc-1', sessionId: 'sub-1' });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });

  // 1024 to 1279: the drawer covers the log column and makes the log inert,
  // so the request has to close it before the card can take the focus.
  it('closes the overlay drawer and shows the log', () => {
    state.value = reduce(initialState(), fx.snapshot);
    drawerOpen.value = true;
    overlayWidth.value = true;
    panelTab.value = 'files';
    vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    expect(logInert.value).toBe(true);

    focusToolCall('s1', 'tc-1');

    expect(drawerOpen.value).toBe(false);
    expect(logInert.value).toBe(false);
    expect(panelTab.value).toBe('log');
  });

  // Below 1024 the side panels share one slot: the drawer is the files tab,
  // so a focus request has to pick the log tab back.
  it('returns from the files tab to the log below 1024', () => {
    state.value = reduce(initialState(), fx.snapshot);
    viewport(true);
    drawerOpen.value = true;
    overlayWidth.value = false;
    panelTab.value = 'files';
    vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('s1', 'tc-1');

    expect(drawerOpen.value).toBe(false);
    expect(panelTab.value).toBe('log');
  });

  it('shows the agent tab for a child session call below 1024', () => {
    state.value = reduce(initialState(), fx.snapshot);
    viewport(true);
    drawerOpen.value = true;
    overlayWidth.value = false;
    panelTab.value = 'files';
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('sub-1', 'sub-tc-1');

    expect(drawerOpen.value).toBe(false);
    expect(panelTab.value).toBe('agent');
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });

  it('keeps the drawer open at 1280 and up so versions can be stepped through', () => {
    state.value = reduce(initialState(), fx.snapshot);
    viewport(false);
    drawerOpen.value = true;
    overlayWidth.value = false;
    panelTab.value = 'files';
    vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('s1', 'tc-1');

    expect(drawerOpen.value).toBe(true);
    expect(toolFocus.value).toEqual({ callId: 'tc-1', sessionId: 's1' });
  });

  it('keeps the overlay drawer open for a child inspector at 1024 to 1279', () => {
    state.value = reduce(initialState(), fx.snapshot);
    viewport(false);
    drawerOpen.value = true;
    overlayWidth.value = true;
    panelTab.value = 'files';
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);

    focusToolCall('sub-1', 'sub-tc-1');

    // The overlay covers the log column, not the inspector in the stage.
    expect(drawerOpen.value).toBe(true);
    expect(panelTab.value).toBe('agent');
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });

  it('keeps a browsed session read-only and starts no work', () => {
    const base = reduce(initialState(), fx.snapshot);
    state.value = {
      ...base,
      browsing: 's-2',
      terminalSession: 's1',
      sessionId: 's-2',
      messages: [{
        id: 'browse-a', role: 'assistant', session_id: 's-2', model: '', provider: '',
        created_at: 1, updated_at: 1,
        parts: [{ type: 'tool_call', data: { id: 'tc-9', name: 'view', input: '{"file_path":"b.go"}' } }],
      }],
    };
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    const send = vi.spyOn(actions, 'sendPrompt');

    focusToolCall('s-2', 'tc-9');

    expect(toolFocus.value).toEqual({ callId: 'tc-9', sessionId: 's-2' });
    expect(state.value.browsing).toBe('s-2');
    expect(state.value.sessionId).toBe('s-2');
    expect(inspect).toHaveBeenCalledWith(null);
    expect(send).not.toHaveBeenCalled();
  });
});

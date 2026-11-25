import { afterEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame } from '../api/types';
import { focusToolCall, toolFocus } from './focusTool';
import { initialState, reduce } from './reducer';
import { actions, state } from './store';

const fx = fixtures as unknown as Record<string, Frame>;

afterEach(() => {
  vi.restoreAllMocks();
  toolFocus.value = null;
  state.value = initialState();
});

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
});

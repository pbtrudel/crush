import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/http';
import { displayDefaults, initialState } from './reducer';
import { state } from './store';
import { actions, displayOpen, effectiveMotion, openDisplay, closeDisplay } from './display';
import { openSessions, sessionsOpen } from './palette';

// flush runs the microtasks the write chain and the setDisplay calls queue.
async function flush(turns = 8) {
  for (let i = 0; i < turns; i++) await Promise.resolve();
}

beforeEach(() => {
  state.value = initialState();
  closeDisplay();
  vi.restoreAllMocks();
});

afterEach(() => {
  closeDisplay();
  sessionsOpen.value = false;
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('effectiveMotion', () => {
  it('takes the config motion over the stored one', () => {
    expect(effectiveMotion({ ...displayDefaults, motion: 'off' }, 'full', false)).toBe('off');
  });

  it('falls back to the stored motion, then the system setting', () => {
    const d = { ...displayDefaults, motion: '' as const };
    expect(effectiveMotion(d, 'reduced', false)).toBe('reduced');
    expect(effectiveMotion(d, null, true)).toBe('reduced');
    expect(effectiveMotion(d, null, false)).toBe('full');
  });
});

describe('actions', () => {
  it('reverts a setting the gateway refused', async () => {
    vi.spyOn(api, 'setConfig').mockRejectedValue(new ApiError(500, 'config_write', 'could not write'));
    expect(state.value.display.theme).toBe('holodeck');
    const failure = await actions.setDisplay('theme', 'solid');
    expect(failure?.tone).toBe('error');
    expect(state.value.display.theme).toBe('holodeck');
  });

  it('sends one setting at a time', async () => {
    const sent: Array<{ key: string; settle: () => void }> = [];
    vi.spyOn(api, 'setConfig').mockImplementation((key) => new Promise((resolve) => {
      sent.push({ key, settle: () => resolve({ ...state.value.display }) });
    }));
    const first = actions.setDisplay('theme', 'solid');
    const second = actions.setDisplay('motion', 'off');
    await flush();
    expect(sent.map((s) => s.key)).toEqual(['options.web_display.theme']);
    sent[0].settle();
    await flush();
    expect(sent.map((s) => s.key)).toEqual(['options.web_display.theme', 'options.web_display.motion']);
    sent[1].settle();
    await Promise.all([first, second]);
  });

  it('removes the four keys one at a time', async () => {
    const removed: string[] = [];
    const remove = vi.spyOn(api, 'removeConfig').mockImplementation(async (key) => {
      removed.push(key);
      return { ...state.value.display };
    });
    await actions.resetDisplay();
    expect(remove).toHaveBeenCalledTimes(4);
    expect(removed).toEqual([
      'options.web_display.theme',
      'options.web_display.motion',
      'options.web_display.low_power',
      'options.web_display.cinematic_hint',
    ]);
    expect(state.value.display).toEqual(displayDefaults);
  });

  it('removes a key rather than writing the empty motion', async () => {
    const remove = vi.spyOn(api, 'removeConfig').mockResolvedValue({ ...state.value.display, motion: '' });
    const set = vi.spyOn(api, 'setConfig');
    await actions.setDisplay('motion', '');
    expect(remove).toHaveBeenCalledWith('options.web_display.motion');
    expect(set).not.toHaveBeenCalled();
  });
});

describe('the display dialog', () => {
  it('closes another panel when it opens', () => {
    openSessions();
    expect(sessionsOpen.value).toBe(true);
    openDisplay();
    expect(sessionsOpen.value).toBe(false);
    expect(displayOpen.value).toBe(true);
  });
});

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
  it('takes the config motion over the system setting', () => {
    expect(effectiveMotion({ ...displayDefaults, motion: 'off' }, false)).toBe('off');
    expect(effectiveMotion({ ...displayDefaults, motion: 'reduced' }, false)).toBe('reduced');
  });

  it('follows the system setting when the config motion is empty', () => {
    const d = { ...displayDefaults, motion: '' as const };
    expect(effectiveMotion(d, true)).toBe('reduced');
    expect(effectiveMotion(d, false)).toBe('full');
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
    const sent: Array<{ key: string; settle: () => void }> = [];
    vi.spyOn(api, 'removeConfig').mockImplementation((key) => new Promise((resolve) => {
      sent.push({ key, settle: () => resolve({ ...state.value.display }) });
    }));
    const done = actions.resetDisplay();
    await flush();
    expect(sent.map((s) => s.key)).toEqual(['options.web_display.theme']);
    sent[0].settle();
    await flush();
    expect(sent.map((s) => s.key)).toEqual(['options.web_display.theme', 'options.web_display.motion']);
    sent[1].settle();
    await flush();
    expect(sent.map((s) => s.key)).toEqual([
      'options.web_display.theme',
      'options.web_display.motion',
      'options.web_display.low_power',
    ]);
    sent[2].settle();
    await flush();
    expect(sent.map((s) => s.key)).toEqual([
      'options.web_display.theme',
      'options.web_display.motion',
      'options.web_display.low_power',
      'options.web_display.cinematic_hint',
    ]);
    sent[3].settle();
    await done;
    expect(state.value.display).toEqual(displayDefaults);
  });

  it('reverts only the key a removal refused', async () => {
    vi.spyOn(api, 'removeConfig').mockImplementation(async (key) => {
      if (key === 'options.web_display.theme') throw new ApiError(500, 'config_write', 'could not write');
      return { ...state.value.display };
    });
    state.value = {
      ...state.value,
      display: { theme: 'solid', motion: 'off', low_power: true, cinematic_hint: false },
    };
    const failure = await actions.resetDisplay();
    expect(failure?.tone).toBe('error');
    expect(state.value.display).toEqual({
      theme: 'solid', motion: '', low_power: false, cinematic_hint: true,
    });
  });

  it('leaves a newer choice alone when an older write fails', async () => {
    const sent: Array<{ resolve: (value: unknown) => void; reject: (err: unknown) => void }> = [];
    vi.spyOn(api, 'setConfig').mockImplementation(() => new Promise((resolve, reject) => {
      sent.push({ resolve: resolve as (value: unknown) => void, reject });
    }));
    const first = actions.setDisplay('motion', 'off');
    const second = actions.setDisplay('motion', 'reduced');
    await flush();
    expect(sent).toHaveLength(1);

    sent[0].reject(new ApiError(500, 'config_write', 'could not write'));
    await flush();
    // The failed write must not put back the value a newer choice replaced.
    expect(state.value.display.motion).toBe('reduced');
    expect(sent).toHaveLength(2);

    sent[1].resolve({ ...state.value.display });
    expect((await first)?.tone).toBe('error');
    await second;
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

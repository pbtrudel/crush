import { afterEach, describe, expect, it } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame } from '../api/types';

// This file imports the display module itself: the push it runs at import
// time is what it checks, and no other test file can observe that first run.
describe('the display push before the snapshot', () => {
  afterEach(() => {
    localStorage.clear();
  });

  it('leaves the cached low power alone until the snapshot lands', async () => {
    localStorage.setItem('crush-web-low-power', '1');
    const { lowPower } = await import('./motion');
    const { initialState, reduce } = await import('./reducer');
    const { state } = await import('./store');
    expect(lowPower.value).toBe(true);

    state.value = initialState();
    expect(state.value.hydrated).toBe(false);
    await import('./display');
    // The config's defaults must not put a low-power tab back on the 3D scene.
    expect(lowPower.value).toBe(true);

    state.value = reduce(initialState(), fixtures.snapshot as unknown as Frame);
    expect(state.value.hydrated).toBe(true);
    // The snapshot names low_power false, which is the config's answer.
    expect(lowPower.value).toBe(false);
  });
});

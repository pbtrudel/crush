import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { Frame, Snapshot } from '../api/types';
import './display';
import { motion } from './motion';
import { displayDefaults, initialState, reduce } from './reducer';
import { state } from './store';

const fx = fixtures as unknown as Record<string, Frame>;

// hydrateFollowSystem lands a snapshot whose display follows the system, so
// the push has to ask the browser what that means.
function hydrateFollowSystem(): void {
  const display = { ...displayDefaults, motion: '' as const };
  const payload: Snapshot = { ...(fx.snapshot.payload as Snapshot), display };
  state.value = reduce(initialState(), { seq: 1, type: 'snapshot', payload } as Frame);
}

// stubMatchMedia takes the browser's reduced-motion answer, following the
// shape overlay.test.ts uses.
function stubMatchMedia(matches: boolean): void {
  window.matchMedia = vi.fn(() => ({ matches } as MediaQueryList)) as unknown as typeof window.matchMedia;
}

const originalMatchMedia = window.matchMedia;

beforeEach(() => {
  localStorage.clear();
  state.value = initialState();
  // A motion the push must overwrite proves the push ran.
  motion.value = 'off';
});

afterEach(() => {
  window.matchMedia = originalMatchMedia;
  vi.restoreAllMocks();
  state.value = initialState();
  motion.value = 'full';
  localStorage.clear();
});

describe('the system motion preference', () => {
  it('follows a reduce preference when the config motion is empty', () => {
    stubMatchMedia(true);
    hydrateFollowSystem();
    expect(window.matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
    expect(motion.value).toBe('reduced');
  });

  it('stays full when the system has no preference', () => {
    stubMatchMedia(false);
    hydrateFollowSystem();
    expect(motion.value).toBe('full');
  });

  it('stays full when the browser has no matchMedia at all', () => {
    window.matchMedia = undefined as unknown as typeof window.matchMedia;
    hydrateFollowSystem();
    expect(motion.value).toBe('full');
  });
});

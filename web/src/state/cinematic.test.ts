import { describe, expect, it } from 'vitest';
import { cinematicKey, type KeyContext } from './cinematic';

const k = (over: Partial<KeyContext>): KeyContext => ({
  code: 'KeyC', altKey: true, inPrompt: false, dialogOpen: false, active: false, allowed: true, ...over,
});

describe('cinematicKey', () => {
  it('toggles on Alt+C by physical key, including while typing', () => {
    expect(cinematicKey(k({}))).toBe('toggle');
    expect(cinematicKey(k({ inPrompt: true }))).toBe('toggle');
    expect(cinematicKey(k({ active: true }))).toBe('toggle');
  });

  it('ignores C without Alt and other keys with Alt', () => {
    expect(cinematicKey(k({ altKey: false }))).toBeNull();
    expect(cinematicKey(k({ code: 'KeyV' }))).toBeNull();
  });

  it('does nothing when cinematic mode is unavailable', () => {
    expect(cinematicKey(k({ allowed: false }))).toBeNull();
  });

  it('exits on Esc only outside the prompt and with no dialog open', () => {
    const esc = { code: 'Escape', altKey: false, active: true };
    expect(cinematicKey(k(esc))).toBe('exit');
    expect(cinematicKey(k({ ...esc, inPrompt: true }))).toBeNull();
    expect(cinematicKey(k({ ...esc, dialogOpen: true }))).toBeNull();
    expect(cinematicKey(k({ ...esc, active: false }))).toBeNull();
  });
});

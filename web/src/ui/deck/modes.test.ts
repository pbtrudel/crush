import { describe, expect, it } from 'vitest';
import { modeFor, payloadFor } from './modes';

describe('deck modes', () => {
  it('reads a leading bang as shell mode', () => {
    expect(modeFor('!ls -la', null)).toBe('shell');
    expect(modeFor('  !ls', null)).toBe('prompt');
    expect(payloadFor('shell', '!ls -la')).toBe('ls -la');
  });
  it('keeps note mode until it is left', () => {
    expect(modeFor('remember the tests', 'note')).toBe('note');
    expect(modeFor('!ls', 'note')).toBe('note');
  });
  it('sends a prompt unchanged', () => {
    expect(payloadFor('prompt', 'hello')).toBe('hello');
  });
});

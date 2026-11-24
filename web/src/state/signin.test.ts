import { afterEach, describe, expect, it } from 'vitest';
import { closeSignIn, escapeSignIn, openSignIn, signInOpen } from './signin';

afterEach(() => {
  closeSignIn();
});

describe('the sign-in dialog open state', () => {
  it('opens with just a provider', () => {
    openSignIn('openai');
    expect(signInOpen.value).toEqual({ provider: 'openai' });
  });

  it('keeps the model the action carries', () => {
    openSignIn('openai', { type: 'main', model: 'gpt-5' });
    expect(signInOpen.value).toEqual({ provider: 'openai', model: { type: 'main', model: 'gpt-5' } });
  });

  it('closes with Escape and reports it consumed the key', () => {
    openSignIn('copilot');
    expect(escapeSignIn()).toBe(true);
    expect(signInOpen.value).toBeNull();
  });

  it('reports Escape when the dialog is closed', () => {
    expect(escapeSignIn()).toBe(false);
  });
});

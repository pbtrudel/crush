import { afterEach, describe, expect, it } from 'vitest';
import {
  closeModels, closePalette, closeSessions, closeSystems, modelsOpen, openModels, openPalette,
  openSessions, openSystems, paletteOpen, sessionsOpen, systemsOpen,
} from './palette';
import { closeSignIn, escapeSignIn, openSignIn, signInModel, signInOpen } from './signin';

afterEach(() => {
  closeSignIn();
  closePalette();
  closeSessions();
  closeModels();
  closeSystems();
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

  it('reads the model the dialog was opened for', () => {
    expect(signInModel()).toBeUndefined();
    openSignIn('openai', { type: 'main', model: 'gpt-5' });
    expect(signInModel()).toEqual({ type: 'main', model: 'gpt-5' });
  });

  it('clears the provider and the model on close', () => {
    openSignIn('openai', { type: 'main', model: 'gpt-5' });
    closeSignIn();
    expect(signInOpen.value).toBeNull();
    expect(signInModel()).toBeUndefined();
  });

  it('closes with Escape and reports it consumed the key', () => {
    openSignIn('copilot');
    expect(escapeSignIn()).toBe(true);
    expect(signInOpen.value).toBeNull();
  });

  it('reports Escape when the dialog is closed', () => {
    expect(escapeSignIn()).toBe(false);
  });

  it('closes every panel it could stack on', () => {
    openPalette('all');
    openSessions();
    openModels();
    openSystems('mcp');

    openSignIn('copilot');

    expect(paletteOpen.value).toBeNull();
    expect(sessionsOpen.value).toBe(false);
    expect(modelsOpen.value).toBe(false);
    expect(systemsOpen.value).toBeNull();
    expect(signInOpen.value).toEqual({ provider: 'copilot' });
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../state/__fixtures__/events.json';
import type { Frame, QuestionRequest, SecurePrompt } from '../api/types';
import { ApiError, api } from '../api/http';
import { cinematic, setCinematic } from '../state/cinematic';
import { resetWebglProbe } from '../state/effects';
import {
  closePalette, closeSessions, deckMode, openPalette, openSessions, paletteOpen, sessionsOpen,
} from '../state/palette';
import { initialState, reduce } from '../state/reducer';
import { closeSignIn, openSignIn, signInOpen } from '../state/signin';
import { actions, state } from '../state/store';
import { App } from './App';

// The Escape routing is the App's: the scene layer would otherwise fail the
// mode closed without a live scene, so the key handler is what this file
// exercises.
vi.mock('./stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');
const original = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
  resetWebglProbe();
  const s = reduce(initialState(), fx.snapshot);
  state.value = { ...s, conn: 'live', inspected: { sessionId: 'sub-1', session: null, messages: [], loading: false } };
  document.body.append(root);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  setCinematic(false);
  closeSessions();
  closeSignIn();
  closePalette();
  deckMode.value = null;
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
  vi.restoreAllMocks();
});

const escape = () => act(() => {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }));
});

describe('Escape in cinematic mode', () => {
  it('closes the inspector first and leaves the mode on the second press', async () => {
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    expect(cinematic.value).toBe(true);

    await escape();
    expect(state.value.inspected).toBeNull();
    expect(cinematic.value).toBe(true);
    expect(document.activeElement?.id).toBe('agent-sub-1');

    await escape();
    expect(cinematic.value).toBe(false);
  });

  it('closes only the display menu when Escape is pressed inside it', async () => {
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    const details = document.querySelector<HTMLDetailsElement>('details.motion-menu')!;
    details.open = true;
    const radio = details.querySelector<HTMLInputElement>('input[type="radio"]')!;
    radio.focus();

    await act(() => { radio.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true })); });

    expect(details.open).toBe(false);
    expect(state.value.inspected?.sessionId).toBe('sub-1');
    expect(cinematic.value).toBe(true);
    expect(document.activeElement).toBe(details.querySelector('summary'));
  });

  it('leaves note mode without closing the inspector or the cinematic view', async () => {
    state.value = { ...state.value, busy: true };
    const cancel = vi.spyOn(actions, 'cancel').mockResolvedValue(undefined);
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    await act(() => { deckMode.value = 'note'; });
    const deck = document.querySelector<HTMLTextAreaElement>('#deck-input')!;
    deck.focus();
    await act(() => {
      deck.value = 'kept draft';
      deck.dispatchEvent(new Event('input', { bubbles: true }));
    });

    await act(() => {
      deck.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true }));
    });

    expect(deckMode.value).toBeNull();
    expect(deck.value).toBe('kept draft');
    expect(state.value.inspected?.sessionId).toBe('sub-1');
    expect(cinematic.value).toBe(true);
    expect(cancel).not.toHaveBeenCalled();
  });

  it('keeps a non-empty message draft, then closes once the box is empty', async () => {
    state.value = { ...state.value, inspected: { sessionId: 'team-m-1', session: null, messages: [], loading: false } };
    await act(() => { render(<App />, root); });
    await act(() => { setCinematic(true); });
    const box = document.querySelector<HTMLTextAreaElement>('#inspector-message')!;
    await act(() => {
      box.value = 'hold that';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    box.focus();

    await act(() => { box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true })); });
    expect(state.value.inspected?.sessionId).toBe('team-m-1');
    expect(cinematic.value).toBe(true);
    expect(box.value).toBe('hold that');

    await act(() => {
      box.value = '';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(() => { box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true })); });
    expect(state.value.inspected).toBeNull();
    expect(cinematic.value).toBe(true);
  });
});

describe('Escape under the interrupt layer', () => {
  const question: QuestionRequest = {
    id: 'b1',
    session_id: 's1',
    tool_call_id: 'tc1',
    questions: [{ id: 'q1', type: 'free_text', question: 'Notes?' }],
  };

  it('cancels the question and leaves the inert dialog open', async () => {
    const cancel = vi.spyOn(actions, 'cancelQuestion').mockResolvedValue(null);
    state.value = { ...state.value, question };
    await act(() => { render(<App />, root); });
    await act(() => { openSessions(); });

    await escape();

    expect(cancel).toHaveBeenCalledTimes(1);
    expect(sessionsOpen.value).toBe(true);
    expect(state.value.inspected?.sessionId).toBe('sub-1');
  });
});

describe('Escape and the sign-in dialog', () => {
  const prompt: SecurePrompt = {
    source: 'pinentry', id: 'p1', kind: 'passphrase', title: 'GPG passphrase', message: 'Unlock the signing key',
  };

  function stubSignIn() {
    vi.spyOn(api, 'signInTargets').mockResolvedValue({ providers: [] });
    vi.spyOn(api, 'signInStatus').mockRejectedValue(new ApiError(404, 'no_flow', 'no sign-in has started'));
  }

  async function mountApp() {
    await act(() => { render(<App />, root); });
  }

  it('closes the sign-in dialog', async () => {
    stubSignIn();
    await mountApp();
    await act(() => { openSignIn('anthropic'); });
    expect(root.querySelector('[role="dialog"][aria-label="Sign in"]')).not.toBeNull();

    await escape();

    expect(signInOpen.value).toBeNull();
  });

  it('closes the sign-in dialog on Ctrl+K and on Cmd+K', async () => {
    for (const meta of [false, true]) {
      stubSignIn();
      await mountApp();
      await act(() => { openSignIn('anthropic'); });
      expect(signInOpen.value).not.toBeNull();

      await act(() => {
        window.dispatchEvent(new KeyboardEvent('keydown', {
          key: 'k', code: 'KeyK', ctrlKey: !meta, metaKey: meta, bubbles: true,
        }));
      });

      expect(signInOpen.value).toBeNull();
      await act(() => { closePalette(); });
    }
  });

  it('closes the palette before the sign-in dialog below it', async () => {
    stubSignIn();
    await mountApp();
    await act(() => { openSignIn('anthropic'); });
    await act(() => { openPalette('all'); });

    await escape();

    expect(paletteOpen.value).toBeNull();
    expect(signInOpen.value).not.toBeNull();
  });

  it('is ignored while a secure prompt owns the key', async () => {
    stubSignIn();
    const cancel = vi.spyOn(actions, 'cancelSecure').mockResolvedValue(null);
    state.value = { ...state.value, securePrompts: [prompt] };
    await mountApp();
    await act(() => { openSignIn('anthropic'); });

    await escape();

    expect(cancel).toHaveBeenCalledTimes(1);
    expect(signInOpen.value).not.toBeNull();
  });
});

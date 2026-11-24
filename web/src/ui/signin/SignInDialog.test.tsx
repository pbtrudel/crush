import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { ApiError, api } from '../../api/http';
import type { SignInResult, SignInState, SignInTarget } from '../../api/types';
import { initialState } from '../../state/reducer';
import { closeModels, modelsOpen } from '../../state/palette';
import { dispatch, state } from '../../state/store';
import { closeSignIn, openSignIn, signInOpen } from '../../state/signin';
import { SignInDialog } from './SignInDialog';

const root = document.createElement('div');

const TARGETS: SignInTarget[] = [
  { id: 'anthropic', name: 'Anthropic', configured: false, methods: ['api_key'] },
  { id: 'copilot', name: 'GitHub Copilot', configured: false, methods: ['oauth'], oauth_kind: 'device' },
  { id: 'openai', name: 'OpenAI', configured: true, methods: ['oauth', 'api_key'], oauth_kind: 'browser' },
];

const DEVICE: SignInState = {
  flow_id: 'f1', provider: 'copilot', provider_name: 'GitHub Copilot', state: 'waiting',
  kind: 'device', user_code: 'WDJB-MJHT', verification_url: 'https://github.com/login/device',
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

async function flush(times = 4) {
  await act(async () => {
    for (let i = 0; i < times; i++) await Promise.resolve();
  });
}

async function mount(inert = false) {
  await act(() => { render(<SignInDialog inert={inert} />, root); });
  await flush();
}

async function unmount() {
  await act(() => { render(null, root); });
  await flush();
}

function layer() {
  return root.querySelector<HTMLElement>('.signin-layer');
}

function dialog() {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="Sign in"]');
}

function listbox() {
  return root.querySelector<HTMLElement>('[role="listbox"][aria-label="Providers"]');
}

function options(): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('.signin__option'));
}

function option(name: string) {
  return options().find((o) => o.querySelector('.signin__name')?.textContent === name);
}

function groupHeads(): string[] {
  return Array.from(root.querySelectorAll<HTMLElement>('.signin__group')).map((g) => g.getAttribute('aria-label') ?? '');
}

function button(name: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => (b.textContent ?? '').trim() === name);
}

function labeled(text: string): HTMLInputElement | null {
  const label = Array.from(root.querySelectorAll<HTMLLabelElement>('label'))
    .find((l) => (l.textContent ?? '').trim() === text && !l.querySelector('input[type="radio"]'));
  if (!label) return null;
  const id = label.getAttribute('for');
  const input = id ? root.querySelector<HTMLInputElement>(`#${id}`) : null;
  return input ?? label.querySelector<HTMLInputElement>('input[type="password"]');
}

function filterInput() {
  return root.querySelector<HTMLInputElement>('input[aria-label="Filter providers"]');
}

function formOf(input: HTMLInputElement | null) {
  return input?.closest('form') ?? null;
}

function alertBox() {
  return root.querySelector<HTMLElement>('[role="alert"]');
}

async function select(name: string) {
  const el = option(name);
  if (!el) throw new Error(`no option ${name}`);
  await act(async () => { el.click(); await Promise.resolve(); });
  await flush();
}

async function click(el: Element | null | undefined) {
  if (!el) throw new Error('no element to click');
  await act(async () => { (el as HTMLElement).click(); await Promise.resolve(); });
  await flush();
}

async function submit(form: Element | null) {
  if (!form) throw new Error('no form');
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await Promise.resolve();
  });
  await flush();
}

beforeEach(() => {
  document.body.append(root);
  state.value = { ...initialState(), conn: 'live' };
  vi.spyOn(api, 'signInTargets').mockResolvedValue({ providers: TARGETS });
  vi.spyOn(api, 'signInStatus').mockRejectedValue(new ApiError(404, 'no_flow', 'no sign-in has started'));
  vi.spyOn(api, 'signInKey').mockResolvedValue({ signed_in: true, model_applied: true });
  vi.spyOn(api, 'signInStart').mockResolvedValue(DEVICE);
  vi.spyOn(api, 'signInCancel').mockResolvedValue(undefined);
  vi.spyOn(api, 'signInCode').mockResolvedValue(undefined);
});

afterEach(async () => {
  await unmount();
  root.remove();
  closeSignIn();
  closeModels();
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('SignInDialog providers', () => {
  it('lists the signed-in providers first and the available ones after', async () => {
    openSignIn();
    await mount();
    expect(dialog()).not.toBeNull();
    expect(listbox()).not.toBeNull();
    expect(groupHeads()).toEqual(['Signed in', 'Available']);
    const optionsIn = (group: string) => Array.from(root.querySelectorAll<HTMLElement>(`.signin__group[aria-label="${group}"] .signin__name`))
      .map((n) => n.textContent);
    expect(optionsIn('Signed in')).toEqual(['OpenAI']);
    expect(optionsIn('Available')).toEqual(['Anthropic', 'GitHub Copilot']);
  });

  it('filters the list by name and provider id', async () => {
    openSignIn();
    await mount();
    const filter = filterInput()!;
    await act(() => {
      filter.value = 'copi';
      filter.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await flush();
    expect(options().map((o) => o.querySelector('.signin__name')!.textContent)).toEqual(['GitHub Copilot']);
  });

  it('shows a providers list failure with a working Retry', async () => {
    vi.mocked(api.signInTargets).mockRejectedValueOnce(
      new ApiError(502, 'providers_unavailable', 'The provider list is not available yet.'),
    );
    openSignIn();
    await mount();
    expect(alertBox()?.textContent).toContain('The provider list is not available yet.');
    await click(button('Retry'));
    expect(options().length).toBe(3);
    expect(alertBox()).toBeNull();
  });

  it('shows the panel of the provider the user picks', async () => {
    openSignIn();
    await mount();
    expect(labeled('API key')).toBeNull();
    await select('Anthropic');
    expect(labeled('API key')).not.toBeNull();
  });

  it('offers both sign-in methods for a provider that takes either', async () => {
    openSignIn('openai');
    await mount();
    const oauth = root.querySelector<HTMLInputElement>('input[name="signin-method"][value="oauth"]')!;
    const key = root.querySelector<HTMLInputElement>('input[name="signin-method"][value="api_key"]')!;
    expect(oauth.checked).toBe(true);
    expect(labeled('API key')).toBeNull();
    await click(key);
    expect(labeled('API key')).not.toBeNull();
  });

  it('marks the dialog inert while another dialog needs you', async () => {
    openSignIn();
    await mount(true);
    expect(layer()!.hasAttribute('inert')).toBe(true);
  });
});

describe('SignInDialog API key', () => {
  it('clears the key before the request settles and keeps it out of state', async () => {
    const pending = deferred<SignInResult>();
    let seen = 'unset';
    vi.mocked(api.signInKey).mockImplementation((_p, key) => {
      seen = labeled('API key')!.value;
      void key;
      return pending.promise;
    });
    openSignIn('anthropic');
    await mount();
    const input = labeled('API key')!;
    expect(input.type).toBe('password');
    expect(input.getAttribute('autocomplete')).toBe('off');
    expect(input.getAttribute('spellcheck')).toBe('false');
    await act(() => {
      input.value = 'sk-ant-123';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(seen).toBe('');
    expect(input.value).toBe('');
    expect(api.signInKey).toHaveBeenCalledWith('anthropic', 'sk-ant-123', undefined);
    expect(JSON.stringify(state.value)).not.toContain('sk-ant-123');
    pending.resolve({ signed_in: true, model_applied: true });
    await flush();
    expect(signInOpen.value).toBeNull();
    expect(state.value.toasts.at(-1)?.text).toBe('Signed in to Anthropic.');
  });

  it('shows a rejected key without echoing it', async () => {
    vi.mocked(api.signInKey).mockRejectedValue(
      new ApiError(422, 'key_rejected', 'The provider did not accept this key.'),
    );
    openSignIn('anthropic');
    await mount();
    const input = labeled('API key')!;
    await act(() => {
      input.value = 'sk-bad';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(alertBox()?.textContent).toContain('The provider did not accept this key.');
    expect(document.body.innerHTML).not.toContain('sk-bad');
    expect(JSON.stringify(state.value)).not.toContain('sk-bad');
    expect(signInOpen.value).not.toBeNull();
  });

  it('passes the model the re-auth toast opened the dialog for', async () => {
    openSignIn('openai', { type: 'main', model: 'gpt-5' });
    await mount();
    await click(root.querySelector('input[name="signin-method"][value="api_key"]'));
    const input = labeled('API key')!;
    await act(() => {
      input.value = 'sk-x';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(api.signInKey).toHaveBeenCalledWith('openai', 'sk-x', { type: 'main', model: 'gpt-5' });
  });

  it('opens the model selector when the model could not be applied', async () => {
    vi.mocked(api.signInKey).mockResolvedValue({ signed_in: true, model_applied: false, reason: 'agent_busy' });
    openSignIn('anthropic');
    await mount();
    const input = labeled('API key')!;
    await act(() => {
      input.value = 'sk-x';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(modelsOpen.value).toBe(true);
    expect(signInOpen.value).toBeNull();
    expect(state.value.toasts.at(-1)?.text).toBe('Crush is busy; choose the model when it finishes.');
  });

  it('leaves the model selector alone when no model was asked for', async () => {
    vi.mocked(api.signInKey).mockResolvedValue({ signed_in: true, model_applied: false });
    openSignIn('anthropic');
    await mount();
    const input = labeled('API key')!;
    await act(() => {
      input.value = 'sk-x';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(modelsOpen.value).toBe(false);
    expect(signInOpen.value).toBeNull();
  });
});

describe('SignInDialog OAuth', () => {
  it('starts the sign-in with the model the dialog was opened for', async () => {
    openSignIn('openai', { type: 'main', model: 'gpt-5' });
    await mount();
    await click(button('Start sign-in'));
    expect(api.signInStart).toHaveBeenCalledWith('openai', { type: 'main', model: 'gpt-5' });
  });

  it('shows the challenge the start answered with', async () => {
    openSignIn('copilot');
    await mount();
    await click(button('Start sign-in'));
    expect(root.textContent).toContain('WDJB-MJHT');
  });

  it('restores the device code from status on open', async () => {
    vi.mocked(api.signInStatus).mockResolvedValue(DEVICE);
    openSignIn();
    await mount();
    expect(root.textContent).toContain('WDJB-MJHT');
  });

  it('shows the code in the HUD numerals with a Copy code button', async () => {
    dispatch((s) => ({ ...s, signin: DEVICE }));
    openSignIn('copilot');
    await mount();
    const code = root.querySelector<HTMLElement>('.signin__code-value');
    expect(code?.textContent).toBe('WDJB-MJHT');
    expect(code?.classList.contains('signin__code-value')).toBe(true);
    expect(button('Copy code')).toBeDefined();
  });

  it('copies the device code', async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: write }, configurable: true });
    dispatch((s) => ({ ...s, signin: DEVICE }));
    openSignIn('copilot');
    await mount();
    await click(button('Copy code'));
    expect(write).toHaveBeenCalledWith('WDJB-MJHT');
  });

  it('selects the code when the clipboard refuses it', async () => {
    const write = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: write }, configurable: true });
    dispatch((s) => ({ ...s, signin: DEVICE }));
    openSignIn('copilot');
    await mount();
    await click(button('Copy code'));
    expect(write).toHaveBeenCalledWith('WDJB-MJHT');
    expect(window.getSelection()?.toString()).toBe('WDJB-MJHT');
  });

  it('opens the provider page through a link the user clicks', async () => {
    dispatch((s) => ({ ...s, signin: DEVICE }));
    openSignIn('copilot');
    await mount();
    const link = Array.from(root.querySelectorAll<HTMLAnchorElement>('a'))
      .find((a) => a.textContent === 'Open the GitHub Copilot sign-in page');
    expect(link).toBeDefined();
    expect(link!.getAttribute('href')).toBe('https://github.com/login/device');
    expect(link!.getAttribute('target')).toBe('_blank');
    expect(link!.getAttribute('rel')).toContain('noopener');
    expect(link!.getAttribute('rel')).toContain('noreferrer');
  });

  it('refuses a verification address that is not a web page', async () => {
    dispatch((s) => ({
      ...s,
      signin: { ...DEVICE, verification_url: 'javascript:alert(1)' },
    }));
    openSignIn('copilot');
    await mount();
    expect(alertBox()?.textContent).toContain('The provider did not return a web address');
    expect(root.querySelectorAll('a').length).toBe(0);
  });

  it('reads the waiting state and the code expiry', async () => {
    const expires = Math.floor(Date.now() / 1000) + 600;
    dispatch((s) => ({
      ...s,
      signin: { ...DEVICE, expires_at: expires },
    }));
    openSignIn('copilot');
    await mount();
    const status = root.querySelector<HTMLElement>('.signin__status');
    expect(status?.getAttribute('role')).toBe('status');
    expect(status?.textContent).toContain('Waiting for GitHub Copilot');
    expect(status?.textContent).toContain('Code expires in 10 minutes');
  });

  it('cancels a running flow and returns focus to the provider list', async () => {
    dispatch((s) => ({
      ...s,
      signin: {
        flow_id: 'f1', provider: 'openai', provider_name: 'OpenAI', state: 'waiting', kind: 'browser',
        verification_url: 'http://localhost:1455/start',
      },
    }));
    openSignIn('openai');
    await mount();
    await click(button('Cancel sign-in'));
    expect(api.signInCancel).toHaveBeenCalledWith('f1');
    expect(document.activeElement?.closest('[role="listbox"]')).not.toBeNull();
  });

  it('treats a cancel for a finished flow as done', async () => {
    vi.mocked(api.signInCancel).mockRejectedValue(new ApiError(404, 'no_flow', 'no such sign-in is running'));
    dispatch((s) => ({ ...s, signin: { ...DEVICE } }));
    openSignIn('copilot');
    await mount();
    await click(button('Cancel sign-in'));
    expect(alertBox()).toBeNull();
  });

  it('clears a pasted code before it is sent', async () => {
    const pending = deferred<void>();
    let seen = 'unset';
    vi.mocked(api.signInCode).mockImplementation((_id, code) => {
      seen = labeled('Code from the sign-in page')!.value;
      void code;
      return pending.promise;
    });
    dispatch((s) => ({
      ...s,
      signin: {
        flow_id: 'f2', provider: 'xai', provider_name: 'xAI', state: 'waiting', kind: 'browser',
        verification_url: 'http://localhost:56121/start', code_entry: true,
      },
    }));
    openSignIn('xai');
    await mount();
    const input = labeled('Code from the sign-in page')!;
    expect(input.type).toBe('password');
    expect(input.getAttribute('autocomplete')).toBe('off');
    await act(() => {
      input.value = 'good-code';
      input.dispatchEvent(new InputEvent('input', { bubbles: true }));
    });
    await submit(formOf(input));
    expect(seen).toBe('');
    expect(input.value).toBe('');
    expect(api.signInCode).toHaveBeenCalledWith('f2', 'good-code');
    expect(JSON.stringify(state.value)).not.toContain('good-code');
    pending.resolve();
    await flush();
  });

  it('shows a refused code sentence from the gateway', async () => {
    await unmount();
    dispatch((s) => ({
      ...s,
      signin: {
        flow_id: 'f2', provider: 'xai', provider_name: 'xAI', state: 'error',
        error: 'That code did not work. Copy the whole code or the address the page showed.',
      },
    }));
    openSignIn('xai');
    await mount();
    expect(alertBox()?.textContent).toContain('That code did not work.');
    expect(button('Try again')).toBeDefined();
  });

  it('shows the finished sign-in with the model action and Close', async () => {
    dispatch((s) => ({
      ...s,
      signin: { ...DEVICE, state: 'done', model_applied: true },
    }));
    openSignIn('copilot');
    await mount();
    expect(root.textContent).toContain('Signed in to GitHub Copilot.');
    await click(button('Choose a model'));
    expect(modelsOpen.value).toBe(true);
    expect(signInOpen.value).toBeNull();
  });

  it('closes a finished sign-in with Close', async () => {
    dispatch((s) => ({ ...s, signin: { ...DEVICE, state: 'done' } }));
    openSignIn('copilot');
    await mount();
    await click(button('Close'));
    expect(signInOpen.value).toBeNull();
  });

  it('calls a cancelled flow cancelled and lets the user try again', async () => {
    dispatch((s) => ({
      ...s,
      signin: { ...DEVICE, state: 'cancelled' },
    }));
    openSignIn('copilot');
    await mount();
    expect(root.textContent).toContain('Sign-in cancelled.');
    await click(button('Try again'));
    expect(api.signInStart).toHaveBeenCalledWith('copilot', undefined);
  });

  it('shows the start failure inline', async () => {
    vi.mocked(api.signInStart).mockRejectedValue(
      new ApiError(409, 'signin_busy', 'Another sign-in is using the callback port. Finish or cancel it first.'),
    );
    openSignIn('copilot');
    await mount();
    await click(button('Start sign-in'));
    expect(alertBox()?.textContent).toContain('Another sign-in is using the callback port.');
  });
});

describe('SignInDialog focus', () => {
  it('gives the opener its focus back when it closes', async () => {
    const opener = document.createElement('button');
    opener.textContent = 'Open sign in';
    document.body.append(opener);
    try {
      openSignIn('anthropic');
      opener.focus();
      await mount();
      expect(dialog()!.contains(document.activeElement)).toBe(true);
      await act(() => { closeSignIn(); });
      await flush();
      expect(signInOpen.value).toBeNull();
      expect(document.activeElement).toBe(opener);
    } finally {
      opener.remove();
    }
  });
});

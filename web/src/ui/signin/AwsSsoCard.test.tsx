import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import fixtures from '../../state/__fixtures__/events.json';
import type { Frame, SecurePrompt } from '../../api/types';
import { api } from '../../api/http';
import { closeSystems, openSystems } from '../../state/palette';
import { initialState, reduce } from '../../state/reducer';
import { state } from '../../state/store';
import { App } from '../App';
import { AwsSsoCard } from './AwsSsoCard';

vi.mock('../stage/SceneLayer', () => ({ SceneLayer: () => null }));

const fx = fixtures as unknown as Record<string, Frame>;
const root = document.createElement('div');

function seed(patch: Partial<typeof state.value> = {}) {
  state.value = { ...initialState(), conn: 'live', ...patch };
}

async function flush(times = 4) {
  await act(async () => {
    for (let i = 0; i < times; i++) await Promise.resolve();
  });
}

async function mount(el = <AwsSsoCard inert={false} />) {
  await act(() => { render(el, root); });
  await flush();
}

async function unmount() {
  await act(() => { render(null, root); });
  await flush();
}

function card() {
  return root.querySelector<HTMLElement>('.aws-sso');
}

function button(name: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button'))
    .find((b) => (b.textContent ?? '').trim() === name);
}

beforeEach(() => {
  document.body.append(root);
  seed();
});

afterEach(async () => {
  await unmount();
  root.remove();
  closeSystems();
  state.value = initialState();
  vi.restoreAllMocks();
});

describe('AwsSsoCard', () => {
  it('shows the command as text and the wait as a status', async () => {
    seed({ awsSso: { command: 'aws sso login --profile dev' } });
    await mount();
    expect(card()?.querySelector('h2')?.textContent).toBe('AWS sign-in');
    expect(card()?.querySelector('code')?.textContent).toBe('aws sso login --profile dev');
    const status = root.querySelector<HTMLElement>('[role="status"]');
    expect(status?.textContent).toBe('Waiting for the AWS CLI to finish');
  });

  it('opens the AWS page through a link with noopener', async () => {
    seed({
      awsSso: {
        command: 'aws sso login --profile dev',
        url: 'https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD',
      },
    });
    await mount();
    const link = root.querySelector<HTMLAnchorElement>('a');
    expect(link?.textContent).toBe('Open the AWS sign-in page');
    expect(link?.getAttribute('href')).toBe('https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD');
    expect(link?.getAttribute('target')).toBe('_blank');
    expect(link?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(root.querySelector('.aws-sso__url')?.getAttribute('aria-live')).toBe('polite');
  });

  it('keeps a non-web address as text', async () => {
    seed({ awsSso: { command: 'aws sso login', url: 'javascript:alert(1)' } });
    await mount();
    expect(root.querySelectorAll('a').length).toBe(0);
    expect(root.textContent).toContain('javascript:alert(1)');
  });

  it('shows the gateway sentence and dismisses the card', async () => {
    seed({ awsSso: { command: 'aws sso login --profile dev', error: 'the SSO session expired' } });
    await mount();
    const alert = root.querySelector<HTMLElement>('[role="alert"]');
    expect(alert?.textContent).toContain('the SSO session expired');
    expect(root.querySelector('[role="status"]')).toBeNull();
    await act(() => { button('Dismiss')!.click(); });
    await flush();
    expect(state.value.awsSso).toBeNull();
    expect(card()).toBeNull();
  });

  it('does not take the focus when it appears', async () => {
    const elsewhere = document.createElement('button');
    document.body.append(elsewhere);
    try {
      elsewhere.focus();
      seed({ awsSso: { command: 'aws sso login', error: 'the SSO session expired' } });
      await mount();
      expect(document.activeElement).toBe(elsewhere);
    } finally {
      elsewhere.remove();
    }
  });

  it('is inert while a prompt needs you', async () => {
    seed({ awsSso: { command: 'aws sso login' } });
    await mount(<AwsSsoCard inert={true} />);
    expect(card()?.hasAttribute('inert')).toBe(true);
  });
});

describe('AwsSsoCard in the app', () => {
  it('comes after the systems panel in the DOM', async () => {
    vi.spyOn(api, 'lspServers').mockResolvedValue({ servers: [] });
    vi.spyOn(api, 'mcpServers').mockResolvedValue({ servers: [], docker: { available: false, enabled: false } });
    state.value = { ...reduce(initialState(), fx.snapshot), conn: 'live', awsSso: { command: 'aws sso login' } };
    await mount(<App /> as never);
    await act(() => { openSystems(); });
    await flush();
    const systems = root.querySelector<HTMLElement>('.systems-layer');
    const aws = root.querySelector<HTMLElement>('.aws-sso');
    expect(systems).not.toBeNull();
    expect(aws).not.toBeNull();
    expect(systems!.compareDocumentPosition(aws!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('is inert while a secure prompt owns the page', async () => {
    const prompt: SecurePrompt = {
      source: 'pinentry', id: 'p1', kind: 'passphrase', title: 'GPG passphrase', message: 'Unlock the key',
    };
    state.value = {
      ...reduce(initialState(), fx.snapshot), conn: 'live', securePrompts: [prompt],
      awsSso: { command: 'aws sso login' },
    };
    await mount(<App /> as never);
    expect(card()?.hasAttribute('inert')).toBe(true);
  });
});

import { describe, expect, it } from 'vitest';
import { toastForAgentEvent } from './notifications';

describe('agent event toasts', () => {
  it('announces a finished turn in another session', () => {
    expect(toastForAgentEvent({ type: 'agent_finished', session_id: 's2', session_title: 'Lexer' }, { currentSession: 's1', hidden: false }))
      .toEqual({ tone: 'info', text: 'Turn complete in “Lexer”' });
  });
  it('stays quiet for the visible session in a visible tab', () => {
    expect(toastForAgentEvent({ type: 'agent_finished', session_id: 's1' }, { currentSession: 's1', hidden: false })).toBeNull();
  });
  it('announces the visible session when the tab is hidden', () => {
    expect(toastForAgentEvent({ type: 'agent_finished', session_id: 's1', session_title: 'Main' }, { currentSession: 's1', hidden: true })?.tone).toBe('info');
  });
  it('shows agent errors', () => {
    expect(toastForAgentEvent({ type: 'error', error: 'rate limited' }, { currentSession: 's1', hidden: false }))
      .toEqual({ tone: 'error', text: 'rate limited' });
  });
  it('offers a browser sign-in when a login expired', () => {
    expect(toastForAgentEvent({ type: 're_authenticate', provider_id: 'hyper' }, { currentSession: 's1', hidden: false }))
      .toEqual({ tone: 'warn', text: 'Your hyper login expired. Sign in again.', action: { kind: 'sign-in', provider: 'hyper' } });
  });
  it('ignores progress events', () => {
    expect(toastForAgentEvent({ type: 'summarize' }, { currentSession: 's1', hidden: false })).toBeNull();
  });
});

describe('agent event toasts beyond the brief', () => {
  const ctx = { currentSession: 's1', hidden: false };

  it('names a finished turn in the visible session by its title', () => {
    expect(toastForAgentEvent({ type: 'agent_finished', session_id: 's1', session_title: 'Main' }, { ...ctx, hidden: true }))
      .toEqual({ tone: 'info', text: 'Turn complete in “Main”' });
  });

  it('announces without a title when the event carries none', () => {
    expect(toastForAgentEvent({ type: 'agent_finished', session_id: 's2' }, ctx))
      .toEqual({ tone: 'info', text: 'Turn complete' });
  });

  it('falls back when an error event carries no text', () => {
    expect(toastForAgentEvent({ type: 'error' }, ctx))
      .toEqual({ tone: 'error', text: 'The agent stopped with an error' });
  });

  it('names no provider when the event carries none', () => {
    expect(toastForAgentEvent({ type: 're_authenticate' }, ctx))
      .toEqual({ tone: 'warn', text: 'Your login expired. Sign in again from the model selector.' });
  });

  it('stays quiet on both AWS SSO events, the card shows them', () => {
    expect(toastForAgentEvent({ type: 'aws_sso_auth', aws_sso_command: 'aws sso login' }, ctx)).toBeNull();
    expect(toastForAgentEvent({ type: 'aws_sso_auth', aws_sso_url: 'https://example.com/sso' }, ctx)).toBeNull();
  });

  it('reports a failed AWS SSO sign-in and announces success', () => {
    expect(toastForAgentEvent({ type: 'aws_sso_auth_result', error: 'expired' }, ctx))
      .toEqual({ tone: 'error', text: 'AWS SSO sign-in failed: expired' });
    expect(toastForAgentEvent({ type: 'aws_sso_auth_result' }, ctx)).toEqual({ tone: 'info', text: 'AWS credentials refreshed.' });
  });

  it('carries the main model in the sign-in action for its provider', () => {
    expect(toastForAgentEvent({ type: 're_authenticate', provider_id: 'openai' }, { ...ctx, model: { provider: 'openai', id: 'gpt-5' } }))
      .toEqual({ tone: 'warn', text: 'Your openai login expired. Sign in again.', action: { kind: 'sign-in', provider: 'openai', model: { type: 'main', model: 'gpt-5' } } });
  });
});

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
  it('sends sign-in requests to the terminal', () => {
    expect(toastForAgentEvent({ type: 're_authenticate', provider_id: 'hyper' }, { currentSession: 's1', hidden: false }))
      .toEqual({ tone: 'warn', text: 'Sign in again from the terminal (hyper)' });
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

  it('leaves out the provider parentheses when there is no provider', () => {
    expect(toastForAgentEvent({ type: 're_authenticate' }, ctx))
      .toEqual({ tone: 'warn', text: 'Sign in again from the terminal' });
  });

  it('toasts the opening AWS SSO event and stays quiet on the follow-up URL', () => {
    expect(toastForAgentEvent({ type: 'aws_sso_auth', aws_sso_command: 'aws sso login' }, ctx))
      .toEqual({ tone: 'warn', text: 'Sign in to AWS SSO from the terminal' });
    expect(toastForAgentEvent({ type: 'aws_sso_auth', aws_sso_url: 'https://example.com/sso' }, ctx)).toBeNull();
  });

  it('reports a failed AWS SSO sign-in and stays quiet on success', () => {
    expect(toastForAgentEvent({ type: 'aws_sso_auth_result', error: 'expired' }, ctx))
      .toEqual({ tone: 'error', text: 'AWS SSO sign-in failed: expired' });
    expect(toastForAgentEvent({ type: 'aws_sso_auth_result' }, ctx)).toBeNull();
  });
});

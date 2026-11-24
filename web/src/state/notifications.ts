import type { AgentEvent } from '../api/types';
import type { Tone } from './reducer';

export interface ToastText {
  tone: Tone;
  text: string;
}

// toastForAgentEvent names the notification an agent event deserves, if any.
// A finished turn is only news when the reader is looking at another session
// or another window: the tab on screen already shows the answer.
export function toastForAgentEvent(
  ev: AgentEvent,
  ctx: { currentSession: string; hidden: boolean },
): ToastText | null {
  switch (ev.type) {
    case 'agent_finished':
      if (ev.session_id === ctx.currentSession && !ctx.hidden) return null;
      return {
        tone: 'info',
        text: ev.session_title ? `Turn complete in “${ev.session_title}”` : 'Turn complete',
      };
    case 'error':
      return { tone: 'error', text: ev.error || 'The agent stopped with an error' };
    case 're_authenticate':
      return {
        tone: 'warn',
        text: ev.provider_id
          ? `Sign in again from the terminal (${ev.provider_id})`
          : 'Sign in again from the terminal',
      };
    case 'aws_sso_auth':
      // The follow-up for the same sign-in carries the verification URL;
      // only the opening event is worth a toast.
      return ev.aws_sso_url ? null : { tone: 'warn', text: 'Sign in to AWS SSO from the terminal' };
    case 'aws_sso_auth_result':
      return ev.error ? { tone: 'error', text: `AWS SSO sign-in failed: ${ev.error}` } : null;
    default:
      return null;
  }
}

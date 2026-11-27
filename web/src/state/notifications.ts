import type { AgentEvent } from '../api/types';
import type { ToastAction, Tone } from './reducer';

export interface ToastText {
  tone: Tone;
  text: string;
  action?: ToastAction;
}

// toastForAgentEvent names the notification an agent event deserves, if any.
// A finished turn is only news when the reader is looking at another session
// or another window: the tab on screen already shows the answer.
export function toastForAgentEvent(
  ev: AgentEvent,
  ctx: { currentSession: string; hidden: boolean; model?: { provider: string; id: string } },
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
    case 're_authenticate': {
      if (!ev.provider_id) {
        return { tone: 'warn', text: 'Your login expired. Sign in again from the model selector.' };
      }
      // The tab reopens sign-in with the model it runs, as the terminal's
      // handleReAuthenticate does with the coder model.
      const model = ctx.model?.provider === ev.provider_id
        ? { type: 'main' as const, model: ctx.model.id }
        : undefined;
      return {
        tone: 'warn',
        text: `Your ${ev.provider_id} login expired. Sign in again.`,
        action: model
          ? { kind: 'sign-in', provider: ev.provider_id, model }
          : { kind: 'sign-in', provider: ev.provider_id },
      };
    }
    case 'aws_sso_auth':
      // The AWS SSO card shows the command and the URL, so neither event
      // needs a toast.
      return null;
    case 'aws_sso_auth_result':
      return ev.error
        ? { tone: 'error', text: `AWS SSO sign-in failed: ${ev.error}` }
        : { tone: 'info', text: 'AWS credentials refreshed.' };
    default:
      return null;
  }
}

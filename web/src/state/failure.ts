import { ApiError } from '../api/http';

export type FailureTone = 'starting' | 'error';

// Failure is what a control shows when a request did not go through.
export interface Failure {
  tone: FailureTone;
  text: string;
}

// STARTING_TEXT names the state every surface shows while the agent boots.
export const STARTING_TEXT = 'Crush is starting the agent. Try again in a moment.';
// RECONNECTING_TEXT is what the gateway answers while its link to the
// terminal is gone: the browser waits that out the way it waits for the
// agent, because the terminal comes back.
const RECONNECTING_TEXT = 'Crush is reconnecting to its server. Try again in a moment.';
const UNREACHABLE_TEXT = 'The request did not reach crush';

// describeFailure names what went wrong in a tone a control can style: a
// starting agent and a reconnecting server are states to wait out, never
// errors.
export function describeFailure(err: unknown): Failure {
  if (err instanceof ApiError) {
    switch (err.code) {
      case 'agent_starting': return { tone: 'starting', text: STARTING_TEXT };
      case 'workspace_gone':
      case 'server_unreachable': return { tone: 'starting', text: RECONNECTING_TEXT };
      default: return { tone: 'error', text: err.message };
    }
  }
  return { tone: 'error', text: UNREACHABLE_TEXT };
}

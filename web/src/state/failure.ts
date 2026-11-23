import { ApiError } from '../api/http';

export type FailureTone = 'starting' | 'error';

// STARTING_TEXT names the state every surface shows while the agent boots.
export const STARTING_TEXT = 'Crush is starting the agent. Try again in a moment.';
const UNREACHABLE_TEXT = 'The request did not reach crush';

// describeFailure names what went wrong in a tone a control can style: a
// starting agent is a state to wait out, never an error.
export function describeFailure(err: unknown): { tone: FailureTone; text: string } {
  if (err instanceof ApiError) {
    return err.code === 'agent_starting'
      ? { tone: 'starting', text: STARTING_TEXT }
      : { tone: 'error', text: err.message };
  }
  return { tone: 'error', text: UNREACHABLE_TEXT };
}

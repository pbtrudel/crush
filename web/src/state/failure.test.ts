import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import { describeFailure } from './failure';

describe('describeFailure', () => {
  it('reads agent_starting as a starting state', () => {
    expect(describeFailure(new ApiError(503, 'agent_starting', 'Crush is starting the agent'))).toEqual({
      tone: 'starting', text: 'Crush is starting the agent. Try again in a moment.',
    });
  });
  it('keeps other server messages', () => {
    expect(describeFailure(new ApiError(409, 'agent_busy', 'the agent is busy'))).toEqual({ tone: 'error', text: 'the agent is busy' });
  });
  it('names a network failure', () => {
    expect(describeFailure(new TypeError('fetch failed')).text).toBe('The request did not reach crush');
  });
});

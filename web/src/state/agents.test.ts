import { describe, expect, it } from 'vitest';
import type { AgentRun, Message, TeamState } from '../api/types';
import { agentTask, currentTool, deriveAgents, scopedTasks, sessionTree } from './agents';

const team = (over: Partial<TeamState> = {}): TeamState => ({
  team_id: 't', tasks: [],
  members: [
    { id: 'lead', name: 'lead', role: 'lead', session_id: 's1' },
    { id: 'm-1', name: 'alice', role: 'teammate', session_id: 'team-m-1', parent_session_id: 's1', status: 'running', model: 'sonnet', model_provider: 'anthropic' },
    { id: 'm-2', name: 'carol', role: 'teammate', session_id: 'team-m-2', parent_session_id: 'team-m-1', status: 'inactive' },
    { id: 'm-3', name: 'bob', role: 'teammate', session_id: 'team-m-3', parent_session_id: 'other-root', status: 'running' },
  ],
  ...over,
});

const run = (over: Partial<AgentRun>): AgentRun => ({
  id: 'r', kind: 'subagent', name: 'Map the parser', session_id: 'sub-1', parent_session_id: 's1',
  status: 'running', started_at: '2026-11-22T12:00:00Z', ...over,
});

describe('sessionTree', () => {
  it('follows spawn links from the root only', () => {
    expect([...sessionTree('s1', [['s1', 'a'], ['a', 'b'], ['x', 'y']])].sort()).toEqual(['a', 'b', 's1']);
    expect(sessionTree('', [['', 'a']]).size).toBe(0);
  });
});

describe('deriveAgents', () => {
  it('scopes the roster to the session tree and merges registry runs', () => {
    const agents = deriveAgents('s1', team(), [run({}), run({ id: 'r9', session_id: 'sub-9', parent_session_id: 'other-root' })]);
    expect(agents.map((a) => a.name)).toEqual(['Map the parser', 'alice', 'carol']);
    expect(agents.find((a) => a.name === 'carol')).toMatchObject({ kind: 'teammate', status: 'inactive' });
  });

  it('orders settled runs newest finish first and breaks a tie on the session id', () => {
    const agents = deriveAgents('s1', team({ members: [] }), [
      run({ id: 'r1', session_id: 'sub-a', status: 'done', started_at: '2026-11-22T12:00:00Z', finished_at: '2026-11-22T12:02:00Z' }),
      run({ id: 'r2', session_id: 'sub-b', status: 'done', started_at: '2026-11-22T12:00:00Z', finished_at: '2026-11-22T12:05:00Z' }),
      run({ id: 'r3', session_id: 'sub-c', status: 'failed', started_at: '2026-11-22T12:00:00Z', finished_at: '2026-11-22T12:02:00Z' }),
    ]);
    expect(agents.map((a) => a.sessionId)).toEqual(['sub-b', 'sub-a', 'sub-c']);
  });

  it('counts a provisioning run as live and starts it after the older ones', () => {
    const agents = deriveAgents('s1', team({ members: [] }), [
      run({ id: 'r1', session_id: 'sub-a', status: 'done', started_at: '2026-11-22T12:00:00Z', finished_at: '2026-11-22T12:05:00Z' }),
      run({ id: 'r2', session_id: 'sub-b', status: 'provisioning', started_at: '2026-11-22T12:03:00Z' }),
      run({ id: 'r3', session_id: 'sub-c', status: 'running', started_at: '2026-11-22T12:01:00Z' }),
    ]);
    expect(agents.map((a) => a.sessionId)).toEqual(['sub-c', 'sub-b', 'sub-a']);
  });

  it('keeps the roster status for teammates and the registry status for subagents', () => {
    const agents = deriveAgents('s1', team(), [
      run({ id: 'tm', kind: 'teammate', name: 'alice', session_id: 'team-m-1', status: 'done', finished_at: '2026-11-22T12:01:00Z' }),
      run({ status: 'failed' }),
    ]);
    expect(agents.find((a) => a.sessionId === 'team-m-1')).toMatchObject({ status: 'running', model: 'sonnet' });
    expect(agents.find((a) => a.sessionId === 'sub-1')?.status).toBe('failed');
  });

  it('maps a failed member to failed', () => {
    const t = team();
    t.members[1] = { ...t.members[1], status: 'inactive', failed: true };
    expect(deriveAgents('s1', t, []).find((a) => a.name === 'alice')?.status).toBe('failed');
  });

  it('drops helper and title sessions', () => {
    const agents = deriveAgents('s1', team({ members: [] }), [run({ session_id: 'title-s1' }), run({ session_id: 'helper-1' })]);
    expect(agents).toEqual([]);
  });

  it('keeps the freshest record of a reopened child session', () => {
    const agents = deriveAgents('s1', team({ members: [] }), [
      run({ id: 'old', status: 'done', started_at: '2026-11-22T11:00:00Z', finished_at: '2026-11-22T11:10:00Z' }),
      run({ id: 'new', status: 'running', started_at: '2026-11-22T12:00:00Z' }),
    ]);
    expect(agents).toHaveLength(1);
    expect(agents[0].status).toBe('running');
  });

  it('is empty without a session', () => {
    expect(deriveAgents('', team(), [run({})])).toEqual([]);
  });
});

describe('currentTool', () => {
  const msg = (over: Partial<Message>): Message => ({
    id: 'a', role: 'assistant', session_id: 'sub-1', model: '', provider: '', created_at: 100, updated_at: 101, parts: [], ...over,
  });

  it('names the newest call that has no result yet', () => {
    const m = msg({ parts: [{ type: 'tool_call', data: { id: 'c1', name: 'grep', input: '{}' } }] });
    expect(currentTool([m])).toEqual({ name: 'grep', since: 101_000 });
  });

  it('is null once the call has a result or the turn ended', () => {
    const call = msg({ parts: [{ type: 'tool_call', data: { id: 'c1', name: 'grep', input: '{}' } }] });
    const result = msg({ id: 't', role: 'tool', parts: [{ type: 'tool_result', data: { tool_call_id: 'c1', name: 'grep', content: '', metadata: '', is_error: false } }] });
    expect(currentTool([call, result])).toBeNull();
    const ended = msg({ parts: [{ type: 'finish', data: { reason: 'end_turn', time: 1 } }] });
    expect(currentTool([ended])).toBeNull();
  });
});

describe('tasks', () => {
  const t = team({
    tasks: [
      { id: 'task-1', revision: 1, subject: 'Port', description: '', status: 'in_progress', owner_id: 'm-1', owner_name: 'alice', blocked_by: [], ready: false, session_id: 's1' },
      { id: 'task-2', revision: 1, subject: 'Other root', description: '', status: 'pending', blocked_by: [], ready: true, session_id: 'other-root' },
      { id: 'task-3', revision: 1, subject: 'Legacy', description: '', status: 'pending', blocked_by: [], ready: true },
    ],
  });

  it('scopes tasks to the session tree and hides tasks without a session', () => {
    expect(scopedTasks('s1', t).map((x) => x.id)).toEqual(['task-1']);
  });

  it('counts a task a run owns beside the members', () => {
    const withRun = team({ tasks: [...t.tasks, { id: 'task-4', revision: 1, subject: 'Run work', description: '', status: 'pending', blocked_by: [], ready: true, session_id: 'sub-1' }] });
    const runs = [run({ id: 'r1', session_id: 'sub-1', parent_session_id: 's1', status: 'running' })];
    expect(scopedTasks('s1', withRun).map((x) => x.id)).toEqual(['task-1']);
    expect(scopedTasks('s1', withRun, runs).map((x) => x.id)).toEqual(['task-1', 'task-4']);
  });

  it('finds the task a teammate owns', () => {
    const alice = deriveAgents('s1', t, []).find((a) => a.name === 'alice')!;
    expect(agentTask(t, alice)?.id).toBe('task-1');
  });
});

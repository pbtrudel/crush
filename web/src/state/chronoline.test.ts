import { describe, expect, it } from 'vitest';
import fixtures from './__fixtures__/events.json';
import type { AgentRun, Frame, Message, Part } from '../api/types';
import { chronoEvents, chronoSignature, targetOf } from './chronoline';
import { initialState, reduce, type State } from './reducer';

const fx = fixtures as unknown as Record<string, Frame>;

function live(messages: Message[] = [], over: Partial<State> = {}): State {
  return { ...reduce(initialState(), fx.snapshot), messages, ...over };
}

function assistant(id: string, session: string, parts: Part[], at = 1): Message {
  return {
    id, role: 'assistant', session_id: session, parts, model: '', provider: '',
    created_at: at, updated_at: at,
  };
}

function call(id: string, name: string, input = '{}'): Part {
  return { type: 'tool_call', data: { id, name, input, finished: true } };
}

describe('chronoEvents', () => {
  it('turns a tool call into one event with the message time', () => {
    const message = (fx.message_assistant.payload as { payload: Message }).payload;
    const events = chronoEvents(live([message]));
    expect(events).toEqual([{
      callId: 'tc-1', sessionId: 's1', tool: 'view', category: 'read', at: 101,
      label: 'parser.go', edit: false,
    }]);
  });

  it('marks a write call as an edit', () => {
    const events = chronoEvents(live([
      assistant('a1', 's1', [call('tc-1', 'edit', '{"file_path":"internal/parser/parser.go"}')], 7),
    ]));
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ tool: 'edit', category: 'write', edit: true, at: 7 });
  });

  it('keeps the newest 200 calls, oldest first', () => {
    const messages = Array.from({ length: 205 }, (_, i) =>
      assistant(`m${i}`, 's1', [call(`tc-${i}`, 'view', `{"file_path":"f${i}.go"}`)], i + 1));
    const events = chronoEvents(live(messages));
    expect(events).toHaveLength(200);
    expect(events[0].callId).toBe('tc-5');
    expect(events.at(-1)!.callId).toBe('tc-204');
    expect(events.map((e) => e.at)).toEqual(Array.from({ length: 200 }, (_, i) => i + 6));
  });

  it('leaves out a call of another session', () => {
    const events = chronoEvents(live([assistant('a1', 'sub-1', [call('tc-1', 'view')])]));
    expect(events).toEqual([]);
  });

  it('returns an empty list when nothing called a tool', () => {
    expect(chronoEvents(live([assistant('a1', 's1', [{ type: 'text', data: { text: 'hi' } }])]))).toEqual([]);
  });

  it('names the child session of an agent call the roster knows', () => {
    const run: AgentRun = {
      id: 'sub-1', kind: 'subagent', name: 'Map the parser', session_id: 'a1$$tc-1',
      parent_session_id: 's1', status: 'running', started_at: '2026-11-22T12:00:00Z',
    };
    const events = chronoEvents(live(
      [assistant('a1', 's1', [call('tc-1', 'agent', '{"prompt":"look"}')])],
      { runs: [run] },
    ));
    expect(events[0].childSessionId).toBe('a1$$tc-1');
  });

  it('names no child session for an agent call the roster does not know', () => {
    const events = chronoEvents(live([assistant('a1', 's1', [call('tc-1', 'agent', '{"prompt":"look"}')])]));
    expect(events[0].childSessionId).toBeUndefined();
  });

  it('reads the browsed session when the tab browses another one', () => {
    const events = chronoEvents(live([
      assistant('a1', 's1', [call('tc-1', 'edit', '{"file_path":"a.go"}')]),
      assistant('b1', 's-2', [call('tc-9', 'view', '{"file_path":"b.go"}')]),
    ], { browsing: 's-2', sessionId: 's-2' }));
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ callId: 'tc-9', sessionId: 's-2', tool: 'view', category: 'read' });
  });
});

describe('chronoSignature', () => {
  it('ignores text updates and changes when a call appears', () => {
    const one = assistant('a1', 's1', [call('tc-1', 'view', '{"file_path":"a.go"}')]);
    const same = { ...one, updated_at: 9, parts: [...one.parts, { type: 'text' as const, data: { text: 'more' } }] };
    expect(chronoSignature(live([one]))).toBe('s1:1:tc-1');
    expect(chronoSignature(live([same]))).toBe('s1:1:tc-1');
    const two = assistant('a2', 's1', [call('tc-2', 'edit', '{"file_path":"b.go"}')], 2);
    expect(chronoSignature(live([one, two]))).toBe('s1:2:tc-2');
    expect(chronoSignature(live([one], { sessionId: 's-2' }))).toBe('s-2:0:');
  });
});

describe('targetOf', () => {
  it('keeps the last segment of a path and leaves commands whole', () => {
    expect(targetOf('internal/parser/parser.go')).toBe('parser.go');
    expect(targetOf('C:\\repo\\a.go')).toBe('a.go');
    expect(targetOf('parser.go')).toBe('parser.go');
    expect(targetOf('go test ./...')).toBe('go test ./...');
    expect(targetOf('')).toBe('');
  });
});

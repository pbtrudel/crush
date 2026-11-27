import { describe, expect, it } from 'vitest';
import { initialState, type State } from './reducer';
import { contextLevel, coreMode } from './levels';
import type { Message } from '../api/types';

const msg = (over: Partial<Message>): Message => ({
  id: 'm', role: 'assistant', session_id: 's', parts: [], model: '', provider: '',
  created_at: 1, updated_at: 1, ...over,
});

describe('contextLevel', () => {
  it('turns amber at 75 percent and magenta at 90 percent', () => {
    expect(contextLevel(0)).toBe('ok');
    expect(contextLevel(0.749)).toBe('ok');
    expect(contextLevel(0.75)).toBe('warn');
    expect(contextLevel(0.899)).toBe('warn');
    expect(contextLevel(0.9)).toBe('critical');
    expect(contextLevel(1)).toBe('critical');
  });

  it('treats NaN as empty', () => {
    expect(contextLevel(Number.NaN)).toBe('ok');
  });
});

describe('coreMode', () => {
  const base: State = { ...initialState(), conn: 'live', sessionId: 's' };

  it('is idle by default and working while busy', () => {
    expect(coreMode(base)).toBe('idle');
    expect(coreMode({ ...base, busy: true })).toBe('working');
  });

  it('needs you while a permission or question waits, even when busy', () => {
    const perm = { id: 'p', session_id: 's', tool_call_id: 't', tool_name: 'bash', description: '', action: '', params: {}, path: '' };
    expect(coreMode({ ...base, busy: true, permissions: [perm] })).toBe('attention');
  });

  it('needs you for a pending plan in plan mode', () => {
    expect(coreMode({ ...base, planReady: 's', inputMode: 'plan' })).toBe('attention');
    expect(coreMode({ ...base, planReady: 's', inputMode: 'code' })).toBe('idle');
    expect(coreMode({ ...base, planReady: 'other', inputMode: 'plan' })).toBe('idle');
  });

  it('shows an error when the last turn finished with an error', () => {
    const failed = msg({ parts: [{ type: 'finish', data: { reason: 'error', time: 1 } }] });
    expect(coreMode({ ...base, messages: [failed] })).toBe('error');
    expect(coreMode({ ...base, busy: true, messages: [failed] })).toBe('working');
  });
});

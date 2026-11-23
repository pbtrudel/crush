import { needsAttention, type State } from './reducer';

export type ContextLevel = 'ok' | 'warn' | 'critical';
export type CoreMode = 'idle' | 'working' | 'attention' | 'error';

export function contextLevel(fill: number): ContextLevel {
  if (!(fill >= 0.75)) return 'ok';
  return fill >= 0.9 ? 'critical' : 'warn';
}

export function coreMode(s: State): CoreMode {
  if (needsAttention(s)) return 'attention';
  if (s.busy) return 'working';
  const last = s.messages.at(-1);
  const failed = last?.role === 'assistant' && last.parts.some((p) => p.type === 'finish' && p.data.reason === 'error');
  return failed ? 'error' : 'idle';
}

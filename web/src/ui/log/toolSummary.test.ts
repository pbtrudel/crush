import { describe, expect, it } from 'vitest';
import { summarizeTool } from './toolSummary';

describe('summarizeTool', () => {
  it('picks the most telling field', () => {
    expect(summarizeTool('bash', '{"command":"go test ./..."}')).toBe('go test ./...');
    expect(summarizeTool('view', '{"file_path":"internal/web/api.go"}')).toBe('internal/web/api.go');
    expect(summarizeTool('grep', '{"pattern":"TODO","path":"src"}')).toBe('TODO');
  });
  it('survives partial JSON while streaming', () => {
    expect(summarizeTool('bash', '{"command":"go te')).toBe('');
  });
  it('truncates long values', () => {
    expect(summarizeTool('bash', JSON.stringify({ command: 'x'.repeat(300) })).length).toBe(121);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Message } from '../../api/types';
import { Transcript } from './Transcript';

vi.mock('./toolSummary', async (orig) => {
  const mod = await orig<typeof import('./toolSummary')>();
  return { ...mod, summarizeTool: vi.fn(mod.summarizeTool) };
});
import { summarizeTool } from './toolSummary';

const root = document.createElement('div');
afterEach(async () => {
  await act(() => { render(null, root); });
  vi.clearAllMocks();
});

const msg = (id: string, role: Message['role'], parts: Message['parts'], at = 1): Message => ({
  id, role, session_id: 's1', parts, model: '', provider: '', created_at: at, updated_at: at,
});

describe('Transcript', () => {
  it('does not re-render an unchanged message', async () => {
    const first = msg('a1', 'assistant', [
      { type: 'tool_call', data: { id: 'c1', name: 'view', input: '{"file_path":"a.go"}' } },
      { type: 'finish', data: { reason: 'tool_use', time: 1 } },
    ]);
    const second = msg('a2', 'assistant', [{ type: 'text', data: { text: 'one' } }], 2);
    await act(() => { render(<Transcript messages={[first, second]} resetKey="s1" label="Log" />, root); });
    const calls = vi.mocked(summarizeTool).mock.calls.length;
    const grown = { ...second, parts: [{ type: 'text' as const, data: { text: 'one two' } }] };
    await act(() => { render(<Transcript messages={[first, grown]} resetKey="s1" label="Log" />, root); });
    expect(vi.mocked(summarizeTool).mock.calls.length).toBe(calls);
    expect(root.textContent).toContain('one two');
  });

  it('pins to the bottom again when the user sends', async () => {
    const many = Array.from({ length: 30 }, (_, i) => msg(`m${i}`, 'assistant', [{ type: 'text', data: { text: `line ${i}` } }], i));
    await act(() => { render(<Transcript messages={many} resetKey="s1" label="Log" />, root); });
    const log = root.querySelector<HTMLElement>('[role="log"]')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 3000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 300 });
    log.scrollTop = 0;
    log.dispatchEvent(new Event('scroll'));
    const sent = [...many, msg('u1', 'user', [{ type: 'text', data: { text: 'next' } }], 99)];
    await act(() => { render(<Transcript messages={sent} resetKey="s1" label="Log" />, root); });
    expect(log.scrollTop).toBe(3000);
  });
});

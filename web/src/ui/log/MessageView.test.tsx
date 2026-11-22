import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Message, ToolResultData } from '../../api/types';
import { MessageView } from './MessageView';

const root = document.createElement('div');
afterEach(async () => { await act(() => { render(null, root); }); });

const call = { type: 'tool_call' as const, data: { id: 'c1', name: 'bash', input: '{"command":"ls"}' } };
const base: Message = { id: 'a', role: 'assistant', session_id: 's1', model: '', provider: '', created_at: 1, updated_at: 1, parts: [call] };

async function show(m: Message, results = new Map<string, ToolResultData>()) {
  await act(() => { render(<MessageView m={m} results={results} />, root); });
  return root.querySelector<HTMLElement>('.tool')!.dataset.status;
}

describe('MessageView', () => {
  it('shows a streaming cursor and a running tool until the turn ends', async () => {
    expect(await show(base)).toBe('running');
    expect(root.querySelector('.msg__cursor')).not.toBeNull();
  });

  it('pairs a tool call with its result', async () => {
    const results = new Map([['c1', { tool_call_id: 'c1', name: 'bash', content: 'ok', metadata: '', is_error: false }]]);
    expect(await show({ ...base, parts: [call, { type: 'finish', data: { reason: 'tool_use', time: 1 } }] }, results)).toBe('done');
  });

  it('marks a call without a result canceled once the turn ends', async () => {
    expect(await show({ ...base, parts: [call, { type: 'finish', data: { reason: 'canceled', time: 1 } }] })).toBe('canceled');
  });

  it('links a remote image instead of loading it', async () => {
    await act(() => {
      render(<MessageView m={{ ...base, parts: [{ type: 'image_url', data: { url: 'https://example.com/a.png' } }] }} results={new Map()} />, root);
    });
    expect(root.querySelector('img')).toBeNull();
    expect(root.querySelector('a')?.getAttribute('href')).toBe('https://example.com/a.png');
  });

  it('clips a long tool input', async () => {
    const long = { ...call, data: { ...call.data, input: JSON.stringify({ command: 'x'.repeat(9000) }) } };
    await act(() => { render(<MessageView m={{ ...base, parts: [long] }} results={new Map()} />, root); });
    expect(root.querySelector('.tool__body pre')!.textContent!.length).toBeLessThanOrEqual(4000);
    expect(root.textContent).toContain('Show full input');
  });

  it('caps the full output and says the terminal has the rest', async () => {
    const huge = 'x'.repeat(300 * 1024);
    const results = new Map([['c1', { tool_call_id: 'c1', name: 'bash', content: huge, metadata: '', is_error: false }]]);
    await act(() => {
      render(<MessageView m={{ ...base, parts: [call, { type: 'finish', data: { reason: 'tool_use', time: 1 } }] }} results={results} />, root);
    });
    const output = () => root.querySelectorAll<HTMLElement>('.tool__body pre')[1]!;
    expect(output().textContent!.length).toBe(4000);
    await act(() => {
      root.querySelectorAll<HTMLButtonElement>('.tool__body button')[0]!
        .dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(output().textContent!.length).toBe(256 * 1024);
    expect(root.textContent).toContain('Showing the first 256 KB');
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Message } from '../../api/types';
import { toolFocus } from '../../state/focusTool';
import { Transcript } from './Transcript';

vi.mock('./toolSummary', async (orig) => {
  const mod = await orig<typeof import('./toolSummary')>();
  return { ...mod, summarizeTool: vi.fn(mod.summarizeTool) };
});
import { summarizeTool } from './toolSummary';

const root = document.createElement('div');
afterEach(async () => {
  await act(() => { render(null, root); });
  root.removeAttribute('inert');
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

  it('scrolls to the bottom when only the reset key changes', async () => {
    const many = Array.from({ length: 30 }, (_, i) => msg(`m${i}`, 'assistant', [{ type: 'text', data: { text: `line ${i}` } }], i));
    await act(() => { render(<Transcript messages={many} resetKey="a" label="Log" />, root); });
    const log = root.querySelector<HTMLElement>('[role="log"]')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 3000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 300 });
    log.scrollTop = 0;
    log.dispatchEvent(new Event('scroll'));
    await act(() => { render(<Transcript messages={many} resetKey="b" label="Log" />, root); });
    expect(log.scrollTop).toBe(3000);
  });

  it('stays unpinned when an assistant message arrives after the user scrolled up', async () => {
    const many = Array.from({ length: 30 }, (_, i) => msg(`m${i}`, 'assistant', [{ type: 'text', data: { text: `line ${i}` } }], i));
    await act(() => { render(<Transcript messages={many} resetKey="s1" label="Log" />, root); });
    const log = root.querySelector<HTMLElement>('[role="log"]')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 3000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 300 });
    log.scrollTop = 0;
    log.dispatchEvent(new Event('scroll'));
    const grown = [...many, msg('a99', 'assistant', [{ type: 'text', data: { text: 'later' } }], 99)];
    await act(() => { render(<Transcript messages={grown} resetKey="s1" label="Log" />, root); });
    expect(log.scrollTop).toBe(0);
  });

  it('does not re-pin when an existing user message is re-rendered', async () => {
    const many = Array.from({ length: 29 }, (_, i) => msg(`m${i}`, 'assistant', [{ type: 'text', data: { text: `line ${i}` } }], i));
    const sent = [...many, msg('u1', 'user', [{ type: 'text', data: { text: 'next' } }], 99)];
    await act(() => { render(<Transcript messages={sent} resetKey="s1" label="Log" />, root); });
    const log = root.querySelector<HTMLElement>('[role="log"]')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 3000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 300 });
    log.scrollTop = 0;
    log.dispatchEvent(new Event('scroll'));
    await act(() => { render(<Transcript messages={[...sent]} resetKey="s1" label="Log" />, root); });
    expect(log.scrollTop).toBe(0);
  });

  it('widens the window to focus a tool card and clears the request', async () => {
    document.body.append(root);
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    const many = Array.from({ length: 250 }, (_, i) => msg(`m${i}`, 'assistant', i === 5
      ? [
        { type: 'tool_call', data: { id: 'tc-old', name: 'view', input: '{"file_path":"old.go"}' } },
        { type: 'finish', data: { reason: 'stop', time: i } },
      ]
      : [{ type: 'text', data: { text: `line ${i}` } }], i));
    toolFocus.value = { callId: 'tc-old', sessionId: 's1' };
    await act(() => {
      render(<Transcript messages={many} resetKey="s1" label="Log" focusCallId="tc-old" />, root);
    });
    // The window grew to hold the message and every message renders.
    expect(root.querySelectorAll('article.msg')).toHaveLength(250);
    const card = root.querySelector<HTMLDetailsElement>('details[data-tool-call-id="tc-old"]')!;
    expect(card.open).toBe(true);
    expect(document.activeElement).toBe(card.querySelector('summary'));
    expect(scroll).toHaveBeenCalled();
    expect(toolFocus.value).toBeNull();
  });

  it('waits for its panel to be visible and inert-free before focusing', async () => {
    document.body.append(root);
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    const one = msg('m1', 'assistant', [
      { type: 'tool_call', data: { id: 'tc-1', name: 'view', input: '{"file_path":"a.go"}' } },
      { type: 'finish', data: { reason: 'stop', time: 1 } },
    ]);
    root.setAttribute('inert', '');
    toolFocus.value = { callId: 'tc-1', sessionId: 's1' };
    await act(() => {
      render(<Transcript messages={[one]} resetKey="s1" label="Log" focusCallId="tc-1" />, root);
    });
    await act(async () => { await new Promise((r) => setTimeout(r, 40)); });
    const card = root.querySelector<HTMLDetailsElement>('details[data-tool-call-id="tc-1"]')!;
    expect(toolFocus.value).not.toBeNull();
    expect(card.open).toBe(false);
    expect(scroll).not.toHaveBeenCalled();
    // The drawer closes and the log leaves inert; the pending request lands.
    root.removeAttribute('inert');
    await act(async () => { await new Promise((r) => setTimeout(r, 40)); });
    expect(card.open).toBe(true);
    expect(document.activeElement).toBe(card.querySelector('summary'));
    expect(scroll).toHaveBeenCalled();
    expect(toolFocus.value).toBeNull();
  });

  it('keeps a pending request when it mounts for that request', async () => {
    const one = msg('m1', 'assistant', [{ type: 'text', data: { text: 'one' } }]);
    toolFocus.value = { callId: 'tc-1', sessionId: 's1' };
    await act(() => { render(<Transcript messages={[one]} resetKey="s1" label="Log" />, root); });
    expect(toolFocus.value).not.toBeNull();
  });

  it('drops a pending request when the session or inspected agent changes', async () => {
    const one = msg('m1', 'assistant', [{ type: 'text', data: { text: 'one' } }]);
    await act(() => { render(<Transcript messages={[one]} resetKey="s1" label="Log" />, root); });
    toolFocus.value = { callId: 'tc-1', sessionId: 's1' };
    await act(() => { render(<Transcript messages={[one]} resetKey="s2" label="Log" />, root); });
    expect(toolFocus.value).toBeNull();
  });

  it('keeps the view in place when earlier messages are revealed', async () => {
    const many = Array.from({ length: 250 }, (_, i) => msg(`m${i}`, 'assistant', [{ type: 'text', data: { text: `line ${i}` } }], i));
    await act(() => { render(<Transcript messages={many} resetKey="s1" label="Log" />, root); });
    const log = root.querySelector<HTMLElement>('[role="log"]')!;
    // scrollHeight follows the rendered message count, so jsdom reports the
    // real before and after heights of the window growth.
    Object.defineProperty(log, 'scrollHeight', {
      configurable: true,
      get: () => log.querySelectorAll('article.msg').length * 15,
    });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 300 });
    log.scrollTop = 1200;
    log.dispatchEvent(new Event('scroll'));
    const before = log.scrollHeight;
    await act(() => {
      root.querySelector<HTMLButtonElement>('.log__earlier')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(root.querySelectorAll('article.msg')).toHaveLength(250);
    expect(log.scrollHeight).toBe(before + 50 * 15);
    expect(log.scrollTop).toBe(1200 + 50 * 15);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { initialState } from '../../state/reducer';
import { state } from '../../state/store';
import type { Message, Part } from '../../api/types';
import { TransmissionLog } from './TransmissionLog';

function message(id: string, session: string, parts: Part[] = []): Message {
  return {
    id, role: 'assistant', session_id: session,
    parts: parts.length ? parts : [
      { type: 'text', data: { text: `message ${id}` } },
      { type: 'finish', data: { reason: 'stop', time: 1 } },
    ],
    model: '', provider: '', created_at: Number(id.slice(1)) || 0, updated_at: 0,
  };
}

function seed(sessionId: string, count: number) {
  const messages = Array.from({ length: count }, (_, i) => message(`m${i}`, sessionId));
  state.value = { ...initialState(), conn: 'live', sessionId, messages };
}

const root = document.createElement('div');

// Preact flushes the mount effect that resets the window on a later tick, so
// let it land before clicking, as it would before a real user could.
async function mount() {
  render(<TransmissionLog />, root);
  await new Promise((r) => setTimeout(r, 30));
}

function clickEarlier() {
  root.querySelector<HTMLButtonElement>('.log__earlier')!
    .dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

afterEach(() => {
  render(null, root);
});

describe('TransmissionLog', () => {
  it('keeps the 200-message window and reveals earlier messages on demand', async () => {
    seed('s1', 250);
    await mount();
    expect(root.querySelectorAll('article.msg')).toHaveLength(200);
    expect(root.querySelector('.log__earlier')?.textContent).toBe('Show 50 earlier messages');
    clickEarlier();
    await vi.waitFor(() => expect(root.querySelectorAll('article.msg')).toHaveLength(250));
    expect(root.querySelector('.log__earlier')).toBeNull();
  });

  it('resets the window when the session changes', async () => {
    seed('s1', 250);
    await mount();
    clickEarlier();
    await vi.waitFor(() => expect(root.querySelectorAll('article.msg')).toHaveLength(250));
    state.value = {
      ...state.value, sessionId: 's2',
      messages: Array.from({ length: 205 }, (_, i) => message(`m${i}`, 's2')),
    };
    await vi.waitFor(() => expect(root.querySelectorAll('article.msg')).toHaveLength(200));
    expect(root.querySelector('.log__earlier')?.textContent).toBe('Show 5 earlier messages');
  });

  it('shows tool output as text, never as HTML', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [
      message('m1', 's1', [
        { type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{"command":"echo"}', finished: true } },
      ]),
      { ...message('t1', 's1', [
        { type: 'tool_result', data: {
          tool_call_id: 'tc-1', name: 'bash', content: '<img src=x onerror="alert(1)">',
          metadata: '', is_error: false,
        } },
      ]), role: 'tool' },
    ] };
    await mount();
    expect(root.querySelectorAll('.tool__body pre')[1]?.textContent).toBe('<img src=x onerror="alert(1)">');
    expect(root.querySelector('.tool__body img')).toBeNull();
  });

  it('explains the empty state once linked', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [] };
    await mount();
    expect(root.querySelector('.log__empty')?.textContent).toContain('No transmissions yet');
  });
});

import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
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

async function mount() {
  await act(() => {
    render(<TransmissionLog />, root);
  });
}

async function clickEarlier() {
  await act(() => {
    root.querySelector<HTMLButtonElement>('.log__earlier')!
      .dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
}

async function update(next: typeof state.value) {
  await act(() => {
    state.value = next;
  });
}

function toolCall(id = 'tc-1'): Part {
  return { type: 'tool_call', data: { id, name: 'bash', input: '{"command":"echo"}', finished: true } };
}

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
});

describe('TransmissionLog', () => {
  it('keeps the 200-message window and reveals earlier messages on demand', async () => {
    seed('s1', 250);
    await mount();
    expect(root.querySelectorAll('article.msg')).toHaveLength(200);
    expect(root.querySelector('.log__earlier')?.textContent).toBe('Show 50 earlier messages');
    await clickEarlier();
    expect(root.querySelectorAll('article.msg')).toHaveLength(250);
    expect(root.querySelector('.log__earlier')).toBeNull();
  });

  it('resets the window when the session changes', async () => {
    seed('s1', 250);
    await mount();
    await clickEarlier();
    expect(root.querySelectorAll('article.msg')).toHaveLength(250);
    await update({
      ...state.value, sessionId: 's2',
      messages: Array.from({ length: 205 }, (_, i) => message(`m${i}`, 's2')),
    });
    expect(root.querySelectorAll('article.msg')).toHaveLength(200);
    expect(root.querySelector('.log__earlier')?.textContent).toBe('Show 5 earlier messages');
  });

  it('pins to the bottom when the session changes', async () => {
    seed('s1', 3);
    await mount();
    const log = root.querySelector<HTMLDivElement>('.log')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 200 });
    await act(() => {
      log.scrollTop = 0;
      log.dispatchEvent(new Event('scroll'));
    });
    // 1000 - 0 - 200 is far from the bottom, so the log is unpinned here.
    await update({ ...state.value, sessionId: 's2', messages: [message('m0', 's2')] });
    expect(log.scrollTop).toBe(1000);
  });

  it('settles an unanswered tool card when the turn finished', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [
      message('m1', 's1', [
        toolCall(),
        { type: 'finish', data: { reason: 'canceled', time: 2 } },
      ]),
    ] };
    await mount();
    expect(root.querySelector('.tool')?.getAttribute('data-status')).toBe('canceled');
    expect(root.querySelector('.tool__status')?.textContent).toBe('Canceled');
  });

  it('keeps an unanswered tool card running while the turn is unfinished', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [
      message('m1', 's1', [toolCall()]),
    ] };
    await mount();
    expect(root.querySelector('.tool')?.getAttribute('data-status')).toBe('running');
  });

  it('keeps a tool card running after a tool_use finish while the result is pending', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [
      message('m1', 's1', [
        toolCall(),
        { type: 'finish', data: { reason: 'tool_use', time: 2 } },
      ]),
    ] };
    await mount();
    expect(root.querySelector('.tool')?.getAttribute('data-status')).toBe('running');
  });

  it('shows tool output as text, never as HTML', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [
      message('m1', 's1', [toolCall()]),
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

  it('announces a ready plan and ends the log with its card', async () => {
    state.value = {
      ...initialState(), conn: 'live', sessionId: 's1', messages: [message('m1', 's1')],
      planReady: 's1', inputMode: 'plan',
    };
    await mount();
    const status = [...root.querySelectorAll<HTMLElement>('[role="status"]')]
      .find((el) => el.textContent === 'The plan is ready');
    expect(status).toBeDefined();
    expect(status!.classList.contains('visually-hidden')).toBe(true);
    const log = root.querySelector<HTMLElement>('.log')!;
    expect(log.lastElementChild!.classList.contains('plan-handoff')).toBe(true);
  });

  it('leaves the plan announcement empty without a pending plan', async () => {
    state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages: [] };
    await mount();
    expect(root.querySelector('.plan-handoff')).toBeNull();
    const statuses = [...root.querySelectorAll('[role="status"]')].map((el) => el.textContent);
    expect(statuses).not.toContain('The plan is ready');
  });
});

import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { AgentRun, Message, Part } from '../../api/types';
import type { State } from '../../state/reducer';
import { initialState } from '../../state/reducer';
import { state } from '../../state/store';

const focused = vi.hoisted(() => ({ calls: [] as Array<[string, string]> }));
vi.mock('../../state/focusTool', () => ({
  focusToolCall: (sessionId: string, callId: string) => { focused.calls.push([sessionId, callId]); },
}));
import { Chronoline } from './Chronoline';

const root = document.createElement('div');
beforeAll(() => {
  document.body.append(root);
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(async () => {
  await act(() => { render(null, root); });
  focused.calls = [];
  state.value = initialState();
});

const AT = new Date(2026, 10, 22, 12, 3, 4).getTime() / 1000;

function message(id: string, parts: Part[], session = 's1', at = AT): Message {
  return {
    id, role: 'assistant', session_id: session, parts, model: '', provider: '',
    created_at: at, updated_at: at,
  };
}

function call(id: string, name: string, input = '{}'): Part {
  return { type: 'tool_call', data: { id, name, input, finished: true } };
}

function seed(messages: Message[], over: Partial<State> = {}) {
  state.value = { ...initialState(), conn: 'live', sessionId: 's1', messages, ...over };
}

function events() {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('.chronoline__event'));
}

describe('Chronoline', () => {
  it('renders nothing when no tool ran', async () => {
    seed([message('a1', [{ type: 'text', data: { text: 'hi' } }])]);
    await act(() => { render(<Chronoline />, root); });
    expect(root.innerHTML).toBe('');
  });

  it('lists one labelled button per event, oldest first, with its category', async () => {
    seed([
      message('a1', [call('tc-1', 'view', '{"file_path":"parser.go"}')]),
      message('a2', [call('tc-2', 'edit', '{"file_path":"tokenizer.go"}')], 's1', AT + 1),
    ]);
    await act(() => { render(<Chronoline />, root); });
    const list = root.querySelector('ol')!;
    expect(list.getAttribute('aria-label')).toBe('Timeline of tool calls');
    const buttons = events();
    expect(buttons.map((b) => b.dataset.callId)).toEqual(['tc-1', 'tc-2']);
    // The category is part of the name, so color is never the only cue.
    expect(buttons[0].getAttribute('aria-label')).toBe('Read view parser.go at 12:03:04');
    expect(buttons[1].getAttribute('aria-label')).toBe('Write edit tokenizer.go at 12:03:05');
  });

  it('moves between events with the arrow keys and one tab stop', async () => {
    seed([
      message('a1', [call('tc-1', 'view', '{"file_path":"parser.go"}')]),
      message('a2', [call('tc-2', 'edit', '{"file_path":"tokenizer.go"}')], 's1', AT + 1),
    ]);
    await act(() => { render(<Chronoline />, root); });
    const buttons = events();
    expect(buttons.map((b) => b.tabIndex)).toEqual([-1, 0]);
    buttons[1].focus();
    await act(() => {
      buttons[1].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    });
    expect(document.activeElement).toBe(buttons[0]);
    expect(events().map((b) => b.tabIndex)).toEqual([0, -1]);
    await act(() => {
      buttons[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    });
    expect(document.activeElement).toBe(buttons[1]);
  });

  it('previews the tool summary on hover and on focus', async () => {
    seed([
      message('a1', [call('tc-1', 'view', '{"file_path":"parser.go"}')]),
      message('a2', [call('tc-2', 'edit', '{"file_path":"tokenizer.go"}')], 's1', AT + 1),
    ]);
    await act(() => { render(<Chronoline />, root); });
    const buttons = events();
    expect(root.querySelector('.chronoline__preview')).toBeNull();
    await act(() => {
      buttons[0].dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
    });
    expect(root.querySelector('.chronoline__preview')!.textContent).toContain('parser.go');
    await act(() => {
      buttons[0].dispatchEvent(new MouseEvent('mouseout', { bubbles: true }));
    });
    expect(root.querySelector('.chronoline__preview')).toBeNull();
    buttons[1].focus();
    await act(() => {});
    expect(root.querySelector('.chronoline__preview')!.textContent).toContain('tokenizer.go');
  });

  it('names the agent an agent call started in its preview', async () => {
    const run: AgentRun = {
      id: 'sub-1', kind: 'subagent', name: 'Map the parser', session_id: 'a1$$tc-1',
      parent_session_id: 's1', status: 'running', started_at: '2026-11-22T12:00:00Z',
    };
    seed([message('a1', [call('tc-1', 'agent', '{"prompt":"look"}')])], { runs: [run] });
    await act(() => { render(<Chronoline />, root); });
    await act(() => {
      events()[0].dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
    });
    expect(root.querySelector('.chronoline__preview')!.textContent).toContain('starts Map the parser');
  });

  it('focuses the tool call a button names', async () => {
    seed([message('a1', [call('tc-1', 'view', '{"file_path":"parser.go"}')])]);
    await act(() => { render(<Chronoline />, root); });
    await act(() => { events()[0].click(); });
    expect(focused.calls).toEqual([['s1', 'tc-1']]);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { api } from '../../api/http';
import { STARTING_TEXT } from '../../state/failure';
import { dispatch, dismissSideAnswer, sideAnswer } from '../../state/store';
import { adoptSession } from '../../state/reducer';
import { SideAnswer } from './SideAnswer';

const root = document.createElement('div');
let deck: HTMLTextAreaElement;

beforeEach(() => {
  document.body.append(root);
  deck = document.createElement('textarea');
  deck.id = 'deck-input';
  document.body.append(deck);
});

afterEach(async () => {
  await act(() => { render(null, root); });
  root.remove();
  deck.remove();
  sideAnswer.value = null;
  vi.restoreAllMocks();
});

async function mount() {
  await act(() => { render(<SideAnswer />, root); });
}

const card = () => root.querySelector<HTMLElement>('.side-answer');

describe('SideAnswer', () => {
  it('shows the pending question then the answer', async () => {
    sideAnswer.value = { question: 'Why the lexer?', answer: null, error: null };
    await mount();

    expect(card()!.textContent).toContain('Side answer, not added to the conversation');
    expect(card()!.textContent).toContain('Why the lexer?');
    expect(card()!.textContent).toContain('Asking...');
    expect(root.querySelector('[role="status"]')!.textContent).toBe('');

    await act(() => {
      sideAnswer.value = { question: 'Why the lexer?', answer: 'Because the tests say so.', error: null };
    });

    expect(card()!.textContent).not.toContain('Asking...');
    expect(card()!.textContent).toContain('Because the tests say so.');
    expect(root.querySelector('[role="status"]')!.textContent).toBe('Side answer ready');
    // The card appears without taking the keyboard away from the deck.
    expect(document.activeElement).not.toBe(card());
  });

  it('renders the answer as sanitized markdown', async () => {
    sideAnswer.value = {
      question: 'Format?',
      answer: '**bold** <script>alert(1)</script> [link](https://example.com)',
      error: null,
    };
    await mount();

    expect(card()!.querySelector('strong')!.textContent).toBe('bold');
    expect(card()!.querySelector('script')).toBeNull();
    expect(card()!.querySelector('a')!.getAttribute('href')).toBe('https://example.com');
  });

  it('dismisses and returns the focus to the deck', async () => {
    sideAnswer.value = { question: 'Why?', answer: 'Because.', error: null };
    await mount();
    deck.focus();

    const dismiss = [...root.querySelectorAll('button')].find((b) => b.textContent === 'Dismiss')!;
    await act(() => { dismiss.click(); });

    expect(sideAnswer.value).toBeNull();
    expect(card()).toBeNull();
    expect(document.activeElement).toBe(deck);
  });

  it('a starting agent shows the starting line, not an error', async () => {
    sideAnswer.value = {
      question: 'Why?', answer: null, error: { tone: 'starting', text: STARTING_TEXT },
    };
    await mount();

    const line = root.querySelector('[role="status"]')!;
    expect(line.textContent).toContain(STARTING_TEXT);
    expect(line.getAttribute('data-tone')).toBe('starting');
    expect([...line.querySelectorAll('button')].some((b) => b.textContent === 'Retry')).toBe(true);
    expect(root.querySelector('[role="alert"]')).toBeNull();
  });

  it('shows another failure as an alert', async () => {
    sideAnswer.value = {
      question: 'Why?', answer: null, error: { tone: 'error', text: 'the model did not answer' },
    };
    await mount();

    const line = root.querySelector('[role="alert"]')!;
    expect(line.textContent).toBe('the model did not answer');
    expect(line.getAttribute('data-tone')).toBe('error');
    expect([...root.querySelectorAll('button')].some((b) => b.textContent === 'Retry')).toBe(false);
  });

  it('asks the same question again from the starting line', async () => {
    const ask = vi.spyOn(api, 'ask').mockResolvedValue({ answer: 'Because.' });
    dispatch((s) => adoptSession(s, 's1'));
    sideAnswer.value = {
      question: 'Why?', answer: null, error: { tone: 'starting', text: STARTING_TEXT },
    };
    await mount();

    const retry = [...root.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!;
    expect(retry).not.toBeUndefined();
    await act(async () => { retry.click(); await Promise.resolve(); });

    expect(ask).toHaveBeenCalledWith('s1', 'Why?');
    expect(sideAnswer.value?.answer).toBe('Because.');
    expect(sideAnswer.value?.error).toBeNull();
  });

  it('keeps the focus on the card when the retry starts a new ask', async () => {
    const answer = new Promise<{ answer: string }>(() => {});
    const ask = vi.spyOn(api, 'ask').mockReturnValue(answer);
    dispatch((s) => adoptSession(s, 's1'));
    sideAnswer.value = {
      question: 'Why?', answer: null, error: { tone: 'starting', text: STARTING_TEXT },
    };
    await mount();

    const retry = [...root.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!;
    retry.focus();
    expect(document.activeElement).toBe(retry);
    await act(() => { retry.click(); });

    expect(ask).toHaveBeenCalledWith('s1', 'Why?');
    expect(document.activeElement).toBe(card());
    expect(card()!.tabIndex).toBe(-1);
  });

  it('renders nothing without an answer', async () => {
    dismissSideAnswer();
    await mount();
    expect(card()).toBeNull();
  });
});

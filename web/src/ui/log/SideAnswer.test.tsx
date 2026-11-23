import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { STARTING_TEXT } from '../../state/failure';
import { dismissSideAnswer, sideAnswer } from '../../state/store';
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
    expect(line.textContent).toBe(STARTING_TEXT);
    expect(line.getAttribute('data-tone')).toBe('starting');
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
  });

  it('renders nothing without an answer', async () => {
    dismissSideAnswer();
    await mount();
    expect(card()).toBeNull();
  });
});

import { describe, expect, it } from 'vitest';
import type { Question } from '../../api/types';
import { buildAnswers, isComplete } from './answers';

const qs: Question[] = [
  { id: 'a', type: 'yes_no', question: 'Proceed?' },
  { id: 'b', type: 'single_choice', question: 'Theme?', choices: [{ id: 'cyan', label: 'Cyan' }] },
  { id: 'c', type: 'multi_choice', question: 'Panels?', choices: [{ id: 'x', label: 'X' }, { id: 'y', label: 'Y' }] },
  { id: 'd', type: 'free_text', question: 'Notes?' },
];

describe('answers', () => {
  it('maps drafts to wire answers', () => {
    expect(buildAnswers(qs, {
      a: { yes: true }, b: { selected: ['cyan'] }, c: { selected: ['x', 'y'] }, d: { text: 'ship it' },
    })).toEqual([
      { question_id: 'a', yes: true },
      { question_id: 'b', selected_ids: ['cyan'] },
      { question_id: 'c', selected_ids: ['x', 'y'] },
      { question_id: 'd', fill_in_text: 'ship it' },
    ]);
  });
  it('a free-text fill-in on a choice question counts as an answer', () => {
    expect(buildAnswers([qs[1]], { b: { text: 'violet' } })).toEqual([{ question_id: 'b', fill_in_text: 'violet' }]);
    expect(isComplete(qs[1], { text: 'violet' })).toBe(true);
  });
  it('completeness', () => {
    expect(isComplete(qs[0], undefined)).toBe(false);
    expect(isComplete(qs[0], { yes: false })).toBe(true);
    expect(isComplete(qs[2], { selected: [] })).toBe(false);
    expect(isComplete(qs[3], { text: '  ' })).toBe(false);
  });
});

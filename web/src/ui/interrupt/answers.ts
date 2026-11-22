import type { Question, QuestionAnswer } from '../../api/types';

export type Draft = { selected?: string[]; text?: string; yes?: boolean };

export function isComplete(q: Question, d?: Draft): boolean {
  if (!d) return false;
  if (d.text && d.text.trim()) return true;
  switch (q.type) {
    case 'yes_no': return d.yes !== undefined;
    case 'single_choice':
    case 'multi_choice': return (d.selected?.length ?? 0) > 0;
    case 'free_text': return false;
  }
}

export function buildAnswers(questions: Question[], draft: Record<string, Draft>): QuestionAnswer[] {
  return questions.map((q) => {
    const d = draft[q.id] ?? {};
    const a: QuestionAnswer = { question_id: q.id };
    if (q.type === 'yes_no' && d.yes !== undefined) a.yes = d.yes;
    if ((q.type === 'single_choice' || q.type === 'multi_choice') && d.selected?.length) a.selected_ids = d.selected;
    if (d.text && d.text.trim()) a.fill_in_text = d.text.trim();
    return a;
  });
}

import { state } from '../../state/store';

const GLYPH: Record<string, string> = { completed: '✓', in_progress: '◐', pending: '○' };
const SPOKEN: Record<string, string> = { completed: 'done', in_progress: 'in progress', pending: 'to do' };
const R = 20;
const C = 2 * Math.PI * R;

export function TodoOrbit() {
  const todos = state.value.session?.todos ?? [];
  if (todos.length === 0) return null;
  const done = todos.filter((t) => t.status === 'completed').length;
  return (
    <section class="todo-orbit holo" aria-labelledby="todo-title">
      <h2 id="todo-title" class="hud-label">Todos <span>{done}/{todos.length}</span></h2>
      <svg class="todo-orbit__ring" viewBox="-24 -24 48 48" aria-hidden="true">
        <circle class="todo-orbit__track" r={R} />
        <circle class="todo-orbit__fill" r={R} stroke-dasharray={`${(C * done) / todos.length} ${C}`} transform="rotate(-90)" />
      </svg>
      <ol class="todo-orbit__list">
        {todos.map((t, i) => (
          <li key={`${i}-${t.content}`} data-status={t.status}>
            <span aria-hidden="true">{GLYPH[t.status] ?? '○'}</span>
            <span>{t.status === 'in_progress' && t.active_form ? t.active_form : t.content}</span>
            <span class="visually-hidden">, {SPOKEN[t.status] ?? t.status}</span>
          </li>
        ))}
      </ol>
    </section>
  );
}

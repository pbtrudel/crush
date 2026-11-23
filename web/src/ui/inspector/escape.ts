import { actions, state } from '../../state/store';

// A draft in the inspector's message box is worth more than one keypress:
// Escape leaves it alone until the box is empty. An IME also owns Escape
// while it is composing.
export function escapeLeavesDraft(e: KeyboardEvent): boolean {
  const target = e.target;
  if (!(target instanceof HTMLTextAreaElement)) return false;
  if (target.closest('.panel--inspector') === null) return false;
  return e.isComposing || target.value.trim() !== '';
}

// closeInspector is the one way out, shared by the panel's own Escape and
// the App's capture handler. It clears the selection and gives focus back to
// the agent's option, or to the list's own tab stop when the agent has left
// the roster.
export function closeInspector(): void {
  const sid = state.value.inspected?.sessionId;
  void actions.inspect(null);
  if (sid === undefined) return;
  const option = document.getElementById(`agent-${sid || 'main'}`);
  if (option) {
    option.focus();
    return;
  }
  const fallback =
    document.querySelector<HTMLElement>('.agent-list [role="option"][tabindex="0"]') ??
    document.querySelector<HTMLElement>('.agent-list');
  if (!fallback) return;
  // A listbox is not a tab stop on its own; make it focusable when no
  // option is left to take the focus.
  if (!fallback.hasAttribute('tabindex')) fallback.tabIndex = -1;
  fallback.focus();
}

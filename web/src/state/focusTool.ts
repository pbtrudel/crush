import { signal } from '@preact/signals';
import { actions, state } from './store';

export interface ToolFocus {
  callId: string;
  sessionId: string;
}

// toolFocus is the one request behind the chronoline and the drawer's version
// links: the transcript that holds the card scrolls to it, opens it and
// focuses it, then clears the request.
export const toolFocus = signal<ToolFocus | null>(null);

// focusToolCall selects the agent that ran a tool call. A call in the shown
// session is the main agent's, so any child inspector closes and the log
// keeps the view; a child's call opens that agent's inspector, whose
// transcript takes the focus request.
export function focusToolCall(sessionId: string, callId: string): void {
  toolFocus.value = { callId, sessionId };
  void actions.inspect(sessionId === state.value.sessionId ? null : sessionId);
}

import { signal } from '@preact/signals';
import { closeDrawer, panelTab } from './drawer';
import { actions, state } from './store';

export interface ToolFocus {
  callId: string;
  sessionId: string;
}

// toolFocus is the one request behind the chronoline and the drawer's version
// links: the transcript that holds the card scrolls to it, opens it and
// focuses it, then clears the request.
export const toolFocus = signal<ToolFocus | null>(null);

// focusToolCall selects the agent that ran a tool call. The drawer covers the
// log between 1024 and 1279px and takes the narrow panel slot below 1024px,
// so the request leaves it first: the log tab for a main-agent call, the
// agent tab for a child's, whose inspector then takes the request.
export function focusToolCall(sessionId: string, callId: string): void {
  const child = sessionId !== state.value.sessionId;
  closeDrawer();
  panelTab.value = child ? 'agent' : 'log';
  toolFocus.value = { callId, sessionId };
  void actions.inspect(child ? sessionId : null);
}

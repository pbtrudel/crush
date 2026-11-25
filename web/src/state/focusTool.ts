import { signal } from '@preact/signals';
import { closeDrawer, panelTab } from './drawer';
import { logInert } from './overlay';
import { actions, state } from './store';

export interface ToolFocus {
  callId: string;
  sessionId: string;
}

// toolFocus is the one request behind the chronoline and the drawer's version
// links: the transcript that holds the card scrolls to it, opens it and
// focuses it, then clears the request.
export const toolFocus = signal<ToolFocus | null>(null);

// narrowPanel reports the single-slot layout below 1024px, where the drawer
// takes the slot the log and the inspector share.
function narrowPanel(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(max-width: 1023px)').matches;
}

// focusToolCall selects the agent that ran a tool call. The drawer is closed
// only when it covers the panel that has to take the focus: it lies over the
// log between 1024 and 1279px, and below 1024px it shares the narrow slot.
// At 1280px and up it keeps its own column, so it stays open and the reader
// can step through versions. The tab always follows the target: the log for
// a main-agent call, the agent tab for a child's.
export function focusToolCall(sessionId: string, callId: string): void {
  const child = sessionId !== state.value.sessionId;
  if (narrowPanel() || (!child && logInert.value)) closeDrawer();
  panelTab.value = child ? 'agent' : 'log';
  toolFocus.value = { callId, sessionId };
  void actions.inspect(child ? sessionId : null);
}

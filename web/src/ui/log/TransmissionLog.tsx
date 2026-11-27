import { toolFocus } from '../../state/focusTool';
import { planPending } from '../../state/reducer';
import { state } from '../../state/store';
import { PlanHandoff } from './PlanHandoff';
import { ShellCard } from './ShellCard';
import { Transcript } from './Transcript';

export function TransmissionLog() {
  const { messages, sessionId, conn } = state.value;
  const pending = planPending(state.value);
  // A focus request belongs to the transcript that holds the card, and the
  // inspector reads its own session's request.
  const focus = toolFocus.value;
  return (
    <>
      {/* The card is not a live region, so the announcement is its own
          persistent polite line: empty, then a ready plan. */}
      <p class="visually-hidden" role="status">{pending ? 'The plan is ready' : ''}</p>
      <Transcript
        messages={messages}
        resetKey={sessionId}
        label="Transmissions"
        focusCallId={focus && focus.sessionId === sessionId ? focus.callId : undefined}
        footer={<><ShellCard /><PlanHandoff /></>}
        empty={conn !== 'booting' && (
          <div class="log__empty">
            <p class="hud-label">No transmissions yet</p>
            <p>Type a task in the deck below or in the terminal. Replies, tool calls and the agent's thought layer stream in here.</p>
          </div>
        )}
      />
    </>
  );
}

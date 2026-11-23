import { state } from '../../state/store';
import { ShellCard } from './ShellCard';
import { Transcript } from './Transcript';

export function TransmissionLog() {
  const { messages, sessionId, conn } = state.value;
  return (
    <Transcript
      messages={messages}
      resetKey={sessionId}
      label="Transmissions"
      footer={<ShellCard />}
      empty={conn !== 'booting' && (
        <div class="log__empty">
          <p class="hud-label">No transmissions yet</p>
          <p>Type a task in the deck below or in the terminal. Replies, tool calls and the agent's thought layer stream in here.</p>
        </div>
      )}
    />
  );
}

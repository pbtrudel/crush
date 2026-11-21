import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { state } from '../../state/store';
import { toolResults } from '../../state/reducer';
import { MessageView } from './MessageView';

const WINDOW = 200;

export function TransmissionLog() {
  const { messages, sessionId, conn } = state.value;
  const [limit, setLimit] = useState(WINDOW);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const results = useMemo(() => toolResults(messages), [messages]);
  const visible = messages.filter((m) => m.role !== 'tool');
  const shown = visible.slice(-limit);

  useEffect(() => {
    setLimit(WINDOW);
    pinned.current = true;
  }, [sessionId]);
  useEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const onScroll = () => {
    const el = scroller.current;
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };

  return (
    <div class="log" ref={scroller} onScroll={onScroll} role="log" aria-live="off">
      {visible.length > shown.length && (
        <button type="button" class="link-button log__earlier" onClick={() => setLimit((l) => l + WINDOW)}>
          Show {Math.min(WINDOW, visible.length - shown.length)} earlier messages
        </button>
      )}
      {shown.length === 0 && conn !== 'booting' && (
        <div class="log__empty">
          <p class="hud-label">No transmissions yet</p>
          <p>Type a task in the deck below or in the terminal. Replies, tool calls and the agent's thought layer stream in here.</p>
        </div>
      )}
      {shown.map((m) => <MessageView key={m.id} m={m} results={results} />)}
    </div>
  );
}

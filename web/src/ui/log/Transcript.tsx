import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import type { Message } from '../../api/types';
import { toolResults } from '../../state/reducer';
import { MessageView } from './MessageView';

const WINDOW = 200;
const PIN_SLACK = 80;

export function Transcript({ messages, resetKey, label, empty }: {
  messages: Message[]; resetKey: string; label: string; empty?: ComponentChildren;
}) {
  const [limit, setLimit] = useState(WINDOW);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const lastUser = useRef<string | undefined>(undefined);
  const grownFrom = useRef<number | null>(null);
  const results = useMemo(() => toolResults(messages), [messages]);
  const visible = useMemo(() => messages.filter((m) => m.role !== 'tool'), [messages]);
  const shown = visible.slice(-limit);

  useEffect(() => {
    setLimit(WINDOW);
    pinned.current = true;
    lastUser.current = undefined;
  }, [resetKey]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && grownFrom.current !== null) {
      // Revealing earlier messages prepends them, so keep the content the
      // user is reading at the same offset instead of jumping upwards.
      el.scrollTop += el.scrollHeight - grownFrom.current;
      grownFrom.current = null;
    }
  }, [limit]);

  useEffect(() => {
    // Sending is a request to see the answer, so a new user message pins
    // the view again even when the user had scrolled up.
    const last = visible.at(-1);
    if (last?.role === 'user' && last.id !== lastUser.current) {
      lastUser.current = last.id;
      pinned.current = true;
    }
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [messages, resetKey]);

  const onScroll = () => {
    const el = scroller.current;
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < PIN_SLACK;
  };

  const showEarlier = () => {
    const el = scroller.current;
    grownFrom.current = el ? el.scrollHeight : null;
    setLimit((l) => l + WINDOW);
  };

  return (
    <div class="log" ref={scroller} onScroll={onScroll} role="log" aria-live="off" aria-label={label}>
      {visible.length > shown.length && (
        <button type="button" class="link-button log__earlier" onClick={showEarlier}>
          Show {Math.min(WINDOW, visible.length - shown.length)} earlier messages
        </button>
      )}
      {shown.length === 0 && empty}
      {shown.map((m) => <MessageView key={m.id} m={m} results={results} />)}
    </div>
  );
}

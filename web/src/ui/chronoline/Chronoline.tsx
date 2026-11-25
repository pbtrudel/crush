import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { findAgent } from '../../state/agents';
import { chronoEvents } from '../../state/chronoline';
import { focusToolCall } from '../../state/focusTool';
import { state } from '../../state/store';
import type { ToolCategory } from '../../state/toolCategory';

const CATEGORY: Record<ToolCategory, string> = {
  read: 'Read', write: 'Write', run: 'Run', network: 'Network',
};

// clock renders the message time for the button's name and the strip. It
// pads by hand so the same instant reads the same in every locale.
function clock(at: number): string {
  const date = new Date(at * 1000);
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((n) => String(n).padStart(2, '0'))
    .join(':');
}

// Chronoline is the horizontal timeline of the shown session's tool calls
// and edits above the command deck. It is derived from the messages the tab
// already holds, so nothing new comes from the gateway.
export function Chronoline() {
  const s = state.value;
  const events = useMemo(() => chronoEvents(s), [s.messages, s.sessionId, s.team, s.runs]);
  const [active, setActive] = useState<number | null>(null);
  const [hovered, setHovered] = useState<string | null>(null);
  const list = useRef<HTMLOListElement>(null);
  const tab = active !== null && active < events.length ? active : events.length - 1;

  useEffect(() => {
    // A timeline reads left to right in time, so the newest call sits at the
    // right edge and the strip follows it.
    const el = list.current;
    if (el) el.scrollLeft = el.scrollWidth;
  }, [events.length]);

  if (events.length === 0) return null;

  const move = (to: number) => {
    const count = events.length;
    const next = ((to % count) + count) % count;
    setActive(next);
    const button = list.current?.querySelectorAll<HTMLButtonElement>('.chronoline__event')[next];
    button?.focus();
    button?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  };

  const onKeyDown = (e: KeyboardEvent) => {
    // Alt, Ctrl and Meta chords belong to the app shortcuts and the browser.
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        e.preventDefault();
        move(tab + 1);
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        e.preventDefault();
        move(tab - 1);
        break;
      case 'Home':
        e.preventDefault();
        move(0);
        break;
      case 'End':
        e.preventDefault();
        move(events.length - 1);
        break;
    }
  };

  const preview = hovered === null ? null : events.find((e) => e.callId === hovered) ?? null;
  const started = preview?.childSessionId ? findAgent(s, preview.childSessionId)?.name : undefined;

  return (
    <div class="chronoline">
      {preview && (
        <p class="chronoline__preview" aria-hidden="true">
          <span class="chronoline__preview-tool">{preview.tool}</span>
          {' '}{preview.label || 'No summary'}
          {started && <span> and starts {started}</span>}
        </p>
      )}
      <ol class="chronoline__list" aria-label="Timeline of tool calls" ref={list} onKeyDown={onKeyDown}>
        {events.map((e, i) => {
          const at = clock(e.at);
          return (
            <li key={e.callId}>
              <button
                type="button"
                class="chronoline__event"
                data-category={e.category}
                data-call-id={e.callId}
                tabIndex={i === tab ? 0 : -1}
                aria-label={`${CATEGORY[e.category]} ${e.tool}${e.label ? ` ${e.label}` : ''} at ${at}`}
                onClick={() => focusToolCall(e.sessionId, e.callId)}
                onFocus={() => { setActive(i); setHovered(e.callId); }}
                onBlur={() => setHovered((h) => (h === e.callId ? null : h))}
                onMouseOver={() => setHovered(e.callId)}
                onMouseOut={() => setHovered((h) => (h === e.callId ? null : h))}
              >
                <span class="chronoline__glyph" aria-hidden="true" />
                <span class="chronoline__time" aria-hidden="true">{at}</span>
              </button>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

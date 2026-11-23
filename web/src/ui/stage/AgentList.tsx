import { useEffect, useRef, useState } from 'preact/hooks';
import { agentsOf, statusLabel, type Agent } from '../../state/agents';
import { needsAttention } from '../../state/reducer';
import { actions, state } from '../../state/store';

const GLYPH: Record<Agent['kind'], string> = { main: '⬡', teammate: '⬢', subagent: '◈' };

function label(a: Agent, attention: boolean): string {
  if (a.kind === 'main') return attention ? 'Waiting for you' : a.status === 'running' ? 'Working' : 'Idle';
  return statusLabel(a.status);
}

export function AgentList() {
  const s = state.value;
  const agents = agentsOf(s);
  const attention = needsAttention(s);
  const selected = s.inspected?.sessionId ?? null;
  // The tab stop follows an agent, not an index, so it stays on the same
  // agent when others join or leave the roster.
  const [activeId, setActiveId] = useState<string | null>(null);
  const items = useRef(new Map<string, HTMLLIElement>());
  // A node click or any other inspection moves the selection without the
  // keyboard, so the tab stop follows it instead of the last arrow press.
  useEffect(() => { setActiveId(null); }, [selected]);
  const found = agents.findIndex((a) => a.sessionId === (activeId ?? selected));
  const index = found < 0 ? 0 : found;

  const move = (to: number) => {
    const next = agents[(to + agents.length) % agents.length];
    setActiveId(next.sessionId);
    const el = items.current.get(next.sessionId);
    el?.focus();
    // The row form can be wider than the stage, and focusing an option only
    // scrolls when none of it shows, so bring the whole option into view.
    // jsdom has no scrollIntoView, hence the optional call.
    el?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  };
  const choose = (a: Agent) => {
    setActiveId(a.sessionId);
    void actions.inspect(a.sessionId || null);
  };
  // The key event's own option wins over the roving index: a key pressed on
  // an option selects that agent even when the tab stop sits elsewhere.
  const optionAt = (target: EventTarget | null): Agent | undefined => {
    const el = target instanceof Element ? target.closest('[role="option"]') : null;
    return el ? agents.find((a) => items.current.get(a.sessionId) === el) : undefined;
  };
  const onKeyDown = (e: KeyboardEvent) => {
    // Alt, Ctrl and Meta chords belong to the app shortcuts and the browser.
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        e.preventDefault();
        move(index + 1);
        break;
      case 'ArrowUp':
      case 'ArrowLeft':
        e.preventDefault();
        move(index - 1);
        break;
      case 'Home':
        e.preventDefault();
        move(0);
        break;
      case 'End':
        e.preventDefault();
        move(agents.length - 1);
        break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        choose(optionAt(e.target) ?? agents[index]);
        break;
      case 'Escape':
        // The list owns the pick, so it is the second way out of the
        // inspector beside the panel's own Escape.
        if (selected !== null) {
          e.preventDefault();
          void actions.inspect(null);
        }
        break;
    }
  };

  return (
    <ul class="agent-list" role="listbox" aria-label="Agents" onKeyDown={onKeyDown}>
      {agents.map((a, i) => (
        <li
          key={a.sessionId || 'main'}
          id={`agent-${a.sessionId || 'main'}`}
          ref={(el) => { if (el) items.current.set(a.sessionId, el); else items.current.delete(a.sessionId); }}
          role="option"
          tabIndex={i === index ? 0 : -1}
          aria-selected={a.sessionId === selected}
          data-kind={a.kind}
          data-status={a.kind === 'main' && attention ? 'attention' : a.status}
          onClick={() => choose(a)}
        >
          <span class="agent-list__glyph" aria-hidden="true">{GLYPH[a.kind]}</span>
          <span class="agent-list__name">{a.name}</span>
          <span class="hud-label agent-list__status">{label(a, attention)}</span>
        </li>
      ))}
    </ul>
  );
}

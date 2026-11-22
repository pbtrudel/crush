import { useLayoutEffect, useRef } from 'preact/hooks';
import { statusLabel, type Agent } from '../../state/agents';
import { currentAgentScreens, subscribeAgentScreens, type AgentScreen } from '../../state/layout';
import { actions } from '../../state/store';

// The tags are the pointer targets of the 3D nodes. They are hidden from
// assistive technology: the Agents listbox is the accessible path.
export function AgentTags({ agents, selected }: { agents: Agent[]; selected: string | null }) {
  const root = useRef<HTMLDivElement>(null);
  const apply = (screens: AgentScreen[]) => {
    const el = root.current;
    if (!el) return;
    for (const s of screens) {
      const tag = el.querySelector<HTMLElement>(`[data-agent-node="${CSS.escape(s.id)}"]`);
      if (!tag) continue;
      tag.style.transform = `translate(${s.x}px, ${s.y}px) translate(-50%, -140%)`;
      tag.dataset.visible = String(s.visible);
    }
  };
  useLayoutEffect(() => subscribeAgentScreens(apply), []);
  const ids = agents.map((a) => a.sessionId).join('|');
  // A tag that just mounted has no position yet; give it the last one.
  useLayoutEffect(() => apply(currentAgentScreens()), [ids]);
  return (
    <div class="agent-tags" ref={root} aria-hidden="true">
      {agents.map((a) => (
        <div
          key={a.sessionId}
          class="agent-tag holo"
          data-agent-node={a.sessionId}
          data-kind={a.kind}
          data-status={a.status}
          data-selected={a.sessionId === selected}
          data-visible="false"
          onClick={() => void actions.inspect(a.sessionId)}
        >
          <span class="agent-tag__name">{a.name}</span>
          <span class="agent-tag__status">{statusLabel(a.status)}</span>
        </div>
      ))}
    </div>
  );
}

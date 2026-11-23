import { useLayoutEffect, useRef } from 'preact/hooks';
import { statusLabel, type Agent } from '../../state/agents';
import { currentAgentScreens, stageRect, subscribeAgentScreens, type AgentScreen } from '../../state/layout';
import { actions } from '../../state/store';

// The tags are the pointer targets of the 3D nodes. They are hidden from
// assistive technology: the Agents listbox is the accessible path.
export function AgentTags({ agents, selected }: { agents: Agent[]; selected: string | null }) {
  const root = useRef<HTMLDivElement>(null);
  const apply = (screens: AgentScreen[]) => {
    const el = root.current;
    if (!el) return;
    // A tag is only ever visible over the stage. A node projected onto the
    // deck or the HUD would otherwise put a click target there.
    const box = stageRect.value;
    const shown = new Set<string>();
    for (const s of screens) {
      const tag = el.querySelector<HTMLElement>(`[data-agent-node="${CSS.escape(s.id)}"]`);
      if (!tag) continue;
      tag.style.transform = `translate(${s.x}px, ${s.y}px) translate(-50%, -140%)`;
      const inside =
        box === null ||
        box.width <= 0 ||
        box.height <= 0 ||
        (s.x >= box.left && s.x <= box.right && s.y >= box.top && s.y <= box.bottom);
      tag.dataset.visible = String(s.visible && inside);
      shown.add(s.id);
    }
    // A node stops projecting while it collapses into the core, and nothing
    // publishes it again. Its tag must not stay behind as a ghost.
    for (const tag of el.querySelectorAll<HTMLElement>('[data-agent-node]')) {
      if (!shown.has(tag.dataset.agentNode ?? '')) tag.dataset.visible = 'false';
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

import { useLayoutEffect, useRef } from 'preact/hooks';
import { statusLabel, type Agent } from '../../state/agents';
import { currentAgentScreens, stageRect, subscribeAgentScreens, type AgentScreen } from '../../state/layout';
import { actions } from '../../state/store';

// A tag sits above its node by 140% of its own height. One that lands on the
// task board moves to just above the board, with this much air.
const OFFSET_ABOVE = 1.4;
const BOARD_GAP = 8;

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
    // The task board is docked over the bottom of the stage and takes clicks,
    // so a tag that lands on it covers a lane heading. Its box is read once
    // per pass; only the tags that collide move. A hidden board reports a
    // degenerate rect, which reads as an overlap, so it counts as absent.
    const docked = document.querySelector('.taskboard')?.getBoundingClientRect() ?? null;
    const board = docked !== null && docked.width > 0 && docked.height > 0 ? docked : null;
    // The inspector is docked in the same stacking context and takes clicks,
    // so a node whose point projects onto it would put its tag over the
    // panel's controls. A closed panel is excluded by the selector; a
    // zero-size rect means it has no box yet and counts as absent.
    const inspectorBox = document.querySelector('.panel--inspector:not([hidden])')?.getBoundingClientRect() ?? null;
    const inspector = inspectorBox !== null && inspectorBox.width > 0 && inspectorBox.height > 0 ? inspectorBox : null;
    const shown = new Set<string>();
    for (const s of screens) {
      const tag = el.querySelector<HTMLElement>(`[data-agent-node="${CSS.escape(s.id)}"]`);
      if (!tag) continue;
      let transform = `translate(${s.x}px, ${s.y}px) translate(-50%, -${OFFSET_ABOVE * 100}%)`;
      // A node above the board's top edge parks its tag above itself, so only
      // the nodes at or below the board need the tag measured.
      if (board !== null && s.y >= board.top) {
        const w = tag.offsetWidth;
        const h = tag.offsetHeight;
        const top = s.y - OFFSET_ABOVE * h;
        const hits = h > 0
          && s.x + w / 2 > board.left && s.x - w / 2 < board.right
          && top + h > board.top && top < board.bottom;
        if (hits) {
          // Sit it just above the board, never above the stage it belongs to.
          const raised = Math.max(board.top - BOARD_GAP - h, (box?.top ?? 0) + BOARD_GAP);
          transform = `translate(${s.x}px, ${raised + h}px) translate(-50%, -100%)`;
        }
      }
      tag.style.transform = transform;
      const inside =
        box === null ||
        box.width <= 0 ||
        box.height <= 0 ||
        (s.x >= box.left && s.x <= box.right && s.y >= box.top && s.y <= box.bottom);
      const overInspector =
        inspector !== null &&
        s.x >= inspector.left && s.x <= inspector.right &&
        s.y >= inspector.top && s.y <= inspector.bottom;
      tag.dataset.visible = String(s.visible && inside && !overInspector);
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

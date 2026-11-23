import { effect } from '@preact/signals';
import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { statusLabel, type Agent } from '../../state/agents';
import { currentAgentScreens, stageRect, subscribeAgentScreens, type AgentScreen } from '../../state/layout';
import { actions } from '../../state/store';

// A tag sits above its node by 140% of its own height. One that lands on the
// task board moves to just above the board, with this much air.
const OFFSET_ABOVE = 1.4;
const BOARD_GAP = 8;

// Docked panels that share the tags' stacking context and take clicks. A
// tag whose box touches one would paint over its controls and steal the
// press. A closed or collapsed panel reports a degenerate rect and counts
// as absent.
const OCCLUDERS = ['.panel--inspector:not([hidden])', '.agent-list'];
const NEIGHBOUR_GAP = 4;

interface Rect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

interface Placement {
  tag: HTMLElement;
  x: number;
  y: number;
  top: number;
  w: number;
  h: number;
  raised: boolean;
  lifted: boolean;
  visible: boolean;
}

function solidRect(selector: string): DOMRect | null {
  const r = document.querySelector(selector)?.getBoundingClientRect() ?? null;
  return r !== null && r.width > 0 && r.height > 0 ? r : null;
}

function overlaps(a: Rect, b: Rect): boolean {
  return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
}

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
    // so a tag that lands on it covers a lane heading. Only the tags that
    // collide move.
    const board = solidRect('.taskboard');
    // The inspector and the agent list are docked in the same stacking
    // context and take clicks too, so a tag over either covers its controls.
    const occluders = OCCLUDERS.map(solidRect).filter((r): r is DOMRect => r !== null);
    const shown = new Set<string>();
    // Every layout read (the boxes above, then each tag's own size) happens
    // before the first write. Interleaving them would force a synchronous
    // layout for every tag. Every tag is measured, not only the ones near
    // the board: the neighbour stack needs each box.
    const placements: Placement[] = [];
    for (const s of screens) {
      const tag = el.querySelector<HTMLElement>(`[data-agent-node="${CSS.escape(s.id)}"]`);
      if (!tag) continue;
      const w = tag.offsetWidth;
      const h = tag.offsetHeight;
      let top = s.y - OFFSET_ABOVE * h;
      let raised = false;
      // A node above the board's top edge parks its tag above itself, so
      // only the nodes at or below the board can collide with it.
      if (board !== null && s.y >= board.top) {
        const hits =
          h > 0 &&
          s.x + w / 2 > board.left && s.x - w / 2 < board.right &&
          top + h > board.top && top < board.bottom;
        if (hits) {
          // Sit it just above the board, never above the stage it belongs to.
          top = Math.max(board.top - BOARD_GAP - h, (box?.top ?? 0) + BOARD_GAP);
          raised = true;
        }
      }
      const inside =
        box === null ||
        box.width <= 0 ||
        box.height <= 0 ||
        (s.x >= box.left && s.x <= box.right && s.y >= box.top && s.y <= box.bottom);
      // An unmeasured tag has no box to test, so its anchor point stands in
      // for one.
      const covered = occluders.some((o) =>
        h > 0
          ? overlaps({ left: s.x - w / 2, top, right: s.x + w / 2, bottom: top + h }, o)
          : s.x >= o.left && s.x <= o.right && s.y >= o.top && s.y <= o.bottom,
      );
      placements.push({ tag, x: s.x, y: s.y, top, w, h, raised, lifted: false, visible: s.visible && inside && !covered });
      shown.add(s.id);
    }
    // Two tags on the same spot hide one another's labels and the lower one
    // steals the press. A visible tag whose box meets one already placed
    // climbs above it, as far as its own stage's top edge.
    const stackable = placements.filter((p) => p.visible).sort((a, b) => a.top - b.top);
    const placed: Rect[] = [];
    for (const p of stackable) {
      const mine: Rect = { left: p.x - p.w / 2, top: p.top, right: p.x + p.w / 2, bottom: p.top + p.h };
      // A lift can land the tag on an occluder the read loop cleared, so the
      // placed boxes are rechecked. One pass per tag is enough even for a
      // crowded roster, and it keeps the loop bounded when the stage edge
      // stops the climb.
      for (let i = 0; i < stackable.length; i++) {
        const hit = placed.find((b) => overlaps(mine, b));
        if (!hit) break;
        p.top = Math.max(hit.top - NEIGHBOUR_GAP - p.h, (box?.top ?? 0) + BOARD_GAP);
        p.lifted = true;
        mine.top = p.top;
        mine.bottom = p.top + p.h;
      }
      if (p.lifted && occluders.some((o) => overlaps(mine, o))) {
        p.visible = false;
        continue;
      }
      placed.push(mine);
    }
    for (const p of placements) {
      p.tag.style.transform = p.raised || p.lifted
        ? `translate(${p.x}px, ${p.top + p.h}px) translate(-50%, -100%)`
        : `translate(${p.x}px, ${p.y}px) translate(-50%, -${OFFSET_ABOVE * 100}%)`;
      p.tag.dataset.visible = String(p.visible);
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
  // The scene republishes only when a node moves. With motion off it never
  // does, so every other input to the placement re-runs it here: the
  // inspector opening or closing (the selection), the stage box, and an
  // occluder changing size.
  useLayoutEffect(() => apply(currentAgentScreens()), [selected]);
  useEffect(() => effect(() => { void stageRect.value; apply(currentAgentScreens()); }), []);
  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => apply(currentAgentScreens()));
    for (const sel of ['.panel--inspector', '.agent-list']) {
      const el = document.querySelector(sel);
      if (el) observer.observe(el);
    }
    return () => observer.disconnect();
  }, [selected]);
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

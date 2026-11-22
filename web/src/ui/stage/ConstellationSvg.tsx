import type { AgentStatus } from '../../state/agents';
import { orbit2D } from '../../scene/orbit';

export function ConstellationSvg({ fill, busy, attention, agents, selected, onSelect }: {
  fill: number;
  busy: boolean;
  attention: boolean;
  agents: { id: string; kind: 'teammate' | 'subagent'; status: AgentStatus; name: string }[];
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const r = 70;
  const c = 2 * Math.PI * r;
  return (
    <svg class="constellation-svg" viewBox="-160 -120 320 220" data-busy={busy} data-attention={attention} aria-hidden="true">
      <ellipse class="cs-ring" rx="150" ry="34" cy="70" />
      <ellipse class="cs-ring cs-ring--violet" rx="110" ry="25" cy="70" />
      <ellipse class="cs-ring" rx="70" ry="16" cy="70" />
      <path class="cs-beam" d="M-55 70 L-22 -10 L22 -10 L55 70 Z" />
      <circle class="cs-context" r={r} cy="-20" stroke-dasharray={`${c * fill} ${c}`} transform="rotate(-90 0 -20)" />
      <polygon class="cs-core" points="0,-62 36,-41 36,1 0,22 -36,1 -36,-41" />
      <circle class="cs-core-glow" r="16" cy="-20" />
      {agents.map((a, i) => {
        const p = orbit2D(i, agents.length);
        return (
          <g key={a.id} class="cs-node" data-agent-node={a.id} data-kind={a.kind} data-status={a.status}
            data-selected={a.id === selected} transform={`translate(${p.x} ${p.y})`} onClick={() => onSelect(a.id)}>
            <line class="cs-filament" x1={-p.x} y1={-20 - p.y} x2="0" y2="0" />
            <polygon class="cs-node__hex" points="0,-11 9.5,-5.5 9.5,5.5 0,11 -9.5,5.5 -9.5,-5.5" />
            {a.status === 'failed' && <text class="cs-node__icon" y="4">!</text>}
          </g>
        );
      })}
    </svg>
  );
}

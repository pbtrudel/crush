import { contextLevel } from '../../state/levels';

const SIZE = 56;
const RADIUS = 22;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

// ContextGauge renders the filled share of the context window as a ring.
// The dash pattern is an SVG attribute, not a style, so the CSP allows it.
export function ContextGauge({ fill }: { fill: number }) {
  const f = Number.isFinite(fill) ? Math.min(1, Math.max(0, fill)) : 0;
  const percent = Math.round(f * 100);
  const center = SIZE / 2;
  return (
    <div
      class="gauge"
      role="meter"
      aria-label="Context"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
      aria-valuetext={`${percent} percent`}
      data-level={contextLevel(f)}
    >
      <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true">
        <circle class="gauge__track" cx={center} cy={center} r={RADIUS} />
        <circle class="gauge__value" cx={center} cy={center} r={RADIUS} stroke-dasharray={`${CIRCUMFERENCE * f} ${CIRCUMFERENCE}`} />
        <text class="gauge__text" x={center} y={center} text-anchor="middle" dominant-baseline="central">{percent}%</text>
      </svg>
    </div>
  );
}

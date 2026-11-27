import type { ToolEvent } from '../../state/telemetry';
import type { ToolCategory } from '../../state/toolCategory';
import { CHART_MINUTES, activityBars } from './series';

const WIDTH = 120;
const HEIGHT = 24;
const SLOT = WIDTH / CHART_MINUTES;
const BAR = 6;

const CATEGORIES: readonly ToolCategory[] = ['read', 'write', 'run', 'network'];

const total = (bar: Record<ToolCategory, number>) => CATEGORIES.reduce((n, c) => n + bar[c], 0);

const counts = (bar: Record<ToolCategory, number>) => CATEGORIES.map((c) => `${c} ${bar[c]}`).join(', ');

// ActivityChart stacks the tool calls of each of the last 15 minutes, one
// bar per minute, scaled to the busiest minute.
export function ActivityChart({ events, now }: { events: ToolEvent[]; now: number }) {
  const bars = activityBars(events, now);
  const max = Math.max(1, ...bars.map(total));
  const calls = bars.reduce((n, bar) => n + total(bar), 0);
  return (
    <svg
      class="chart"
      role="img"
      aria-label={`Tool calls in the last 15 minutes: ${calls}`}
      width={WIDTH}
      height={HEIGHT}
      viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
    >
      <line class="chart__base" x1={0} y1={HEIGHT - 0.5} x2={WIDTH} y2={HEIGHT - 0.5} />
      {bars.map((bar, i) => {
        const x = i * SLOT + (SLOT - BAR) / 2;
        let y = HEIGHT;
        const segments = CATEGORIES
          .filter((c) => bar[c] > 0)
          .map((c) => {
            const h = Math.round((bar[c] / max) * HEIGHT);
            y -= h;
            return { category: c, y, h };
          });
        return (
          <g key={i}>
            <title>{counts(bar)}</title>
            {segments.map((s) => (
              <rect key={s.category} class={`bar--${s.category}`} x={x} y={s.y} width={BAR} height={s.h} />
            ))}
          </g>
        );
      })}
    </svg>
  );
}

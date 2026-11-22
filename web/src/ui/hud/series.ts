import type { ToolCategory } from '../../state/toolCategory';
import type { ToolEvent } from '../../state/telemetry';

const MINUTE = 60_000;
export const CHART_MINUTES = 15;

export type ServerTone = 'lit' | 'dim' | 'error';

// sparkPoints maps values onto a polyline in the given box. Fewer than two
// values and a flat series draw the midline, so the line never vanishes.
export function sparkPoints(values: number[], width: number, height: number): string {
  const mid = Math.round(height / 2);
  if (values.length < 2) return `0,${mid} ${width},${mid}`;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const last = values.length - 1;
  return values
    .map((v, i) => {
      const x = Math.round((i * width) / last);
      const y = max === min ? mid : Math.round(height - ((v - min) / (max - min)) * height);
      return `${x},${y}`;
    })
    .join(' ');
}

// activityBars buckets events into one slot per minute, oldest first, and
// drops events outside the window.
export function activityBars(events: ToolEvent[], now: number, minutes = CHART_MINUTES): Record<ToolCategory, number>[] {
  const bars = Array.from({ length: minutes }, () => ({ read: 0, write: 0, run: 0, network: 0 }));
  for (const e of events) {
    const i = minutes - 1 - Math.floor((now - e.at) / MINUTE);
    if (i < 0 || i >= minutes) continue;
    bars[i][e.category] += 1;
  }
  return bars;
}

// serverTone maps the Go state enums to the hex colors. LSP states are
// numeric strings (2 ready, 3 error); MCP states are words.
export function serverTone(kind: 'lsp' | 'mcp', state: string): ServerTone {
  if (kind === 'lsp') return state === '2' ? 'lit' : state === '3' ? 'error' : 'dim';
  if (state === 'connected') return 'lit';
  return state === 'error' || state === 'needs auth' ? 'error' : 'dim';
}

export function digits(text: string): string[] {
  return Array.from(text);
}

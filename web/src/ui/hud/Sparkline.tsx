import { sparkPoints } from './series';

const WIDTH = 60;
const HEIGHT = 16;

// Sparkline is the recent trend behind a numeral. The line is decorative
// and the label names the trend, since the numeral speaks the value.
export function Sparkline({ values, label }: { values: number[]; label: string }) {
  return (
    <span class="spark" role="img" aria-label={label}>
      <svg width={WIDTH} height={HEIGHT} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} aria-hidden="true">
        <polyline points={sparkPoints(values, WIDTH, HEIGHT)} />
      </svg>
    </span>
  );
}

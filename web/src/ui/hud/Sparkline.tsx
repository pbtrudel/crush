import { sparkPoints } from './series';

const WIDTH = 60;
const HEIGHT = 16;

// Sparkline is the recent trend behind a numeral. It stays decorative: the
// numeral's hidden text speaks the value, and a label such as "Token trend"
// would announce no data of its own.
export function Sparkline({ values }: { values: number[] }) {
  return (
    <span class="spark" aria-hidden="true">
      <svg width={WIDTH} height={HEIGHT} viewBox={`0 0 ${WIDTH} ${HEIGHT}`}>
        <polyline points={sparkPoints(values, WIDTH, HEIGHT)} />
      </svg>
    </span>
  );
}

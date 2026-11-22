export function ConstellationSvg({ fill, busy, attention }: { fill: number; busy: boolean; attention: boolean }) {
  const r = 70;
  const c = 2 * Math.PI * r;
  return (
    <svg class="constellation-svg" viewBox="-160 -120 320 220" data-busy={busy} data-attention={attention} role="img" aria-label="Coder agent core">
      <ellipse class="cs-ring" rx="150" ry="34" cy="70" />
      <ellipse class="cs-ring cs-ring--violet" rx="110" ry="25" cy="70" />
      <ellipse class="cs-ring" rx="70" ry="16" cy="70" />
      <path class="cs-beam" d="M-55 70 L-22 -10 L22 -10 L55 70 Z" />
      <circle class="cs-context" r={r} cy="-20" stroke-dasharray={`${c * fill} ${c}`} transform="rotate(-90 0 -20)" />
      <polygon class="cs-core" points="0,-62 36,-41 36,1 0,22 -36,1 -36,-41" />
      <circle class="cs-core-glow" r="16" cy="-20" />
    </svg>
  );
}

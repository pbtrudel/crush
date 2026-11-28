import { clsx } from 'clsx'

import styles from './Usage.module.css'

const radius = 6
const circumference = 2 * Math.PI * radius

// ContextRing is the share of the context window in use. The arc length is
// an SVG presentation attribute, not a style, so the CSP has no say in it.
// The fraction is clamped to the window it describes: an unknown or
// overflowing one draws 0% or 100%, never a negative or non-finite arc.
export default function ContextRing({ fraction }: { fraction: number }) {
  const clamped = Number.isFinite(fraction) ? Math.min(1, Math.max(0, fraction)) : 0
  const percent = Math.round(clamped * 100)
  return (
    <svg
      className={clsx(styles.ring, clamped >= 0.8 && styles.ringHigh)}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      role="img"
      aria-label={`${percent}% of the context window used`}
    >
      <circle className={styles.ringTrack} cx="8" cy="8" r={radius} fill="none" strokeWidth="2" />
      <circle
        className={styles.ringFill}
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        strokeWidth="2"
        strokeDasharray={`${(circumference * clamped).toFixed(2)} ${circumference.toFixed(2)}`}
        transform="rotate(-90 8 8)"
      />
    </svg>
  )
}

import { clsx } from 'clsx'
import { useCallback, useContext, useMemo, useState, useSyncExternalStore } from 'react'

import { clipDiffLines, type DiffLine } from './diff'
import { ToolEnvironmentContext } from './environment'
import styles from './DiffView.module.css'

export interface DiffViewProps {
  lines: DiffLine[]
  // revealKey names this diff in the environment's revealed set, so "Show
  // all" survives the row unmounting. Without one the diff keeps the state
  // itself.
  revealKey?: string
}

// The mark and the spoken word carry a change, so a diff still reads when
// the colour is missing: colour is never the only signal.
const marks: Record<DiffLine['kind'], string> = { hunk: '', marker: '', add: '+', del: '−', context: ' ' }
const spoken: Record<DiffLine['kind'], string> = {
  hunk: '',
  marker: '',
  add: 'Added: ',
  del: 'Removed: ',
  context: '',
}

function thousands(chars: number): number {
  return Math.ceil(chars / 1000)
}

// DiffView draws a diff, one row per line with both line numbers. A long
// diff is clipped the way a long result is, so one huge file cannot stall
// the log.
export default function DiffView({ lines, revealKey }: DiffViewProps) {
  const environment = useContext(ToolEnvironmentContext)
  const [localAll, setLocalAll] = useState(false)
  const readRevealed = useCallback(
    () => revealKey !== undefined && environment.revealed.has(revealKey),
    [environment, revealKey],
  )
  const revealed = useSyncExternalStore(environment.revealed.subscribe, readRevealed)
  const all = revealKey === undefined ? localAll : revealed
  const shown = useMemo(
    () => (all ? { rows: lines, clipped: false, byChars: false } : clipDiffLines(lines)),
    [all, lines],
  )
  if (lines.length === 0) {
    return <p className={styles.empty}>No changes</p>
  }
  const chars = shown.byChars ? lines.reduce((total, line) => total + line.text.length, 0) : 0
  return (
    <div className={styles.view}>
      <pre className={styles.diff}>
        {shown.rows.map((line, index) => (
          <span key={index} className={clsx(styles.line, styles[line.kind])} data-kind={line.kind}>
            <span className={styles.number} aria-hidden="true">
              {line.oldNumber ?? ''}
            </span>
            <span className={styles.number} aria-hidden="true">
              {line.newNumber ?? ''}
            </span>
            <span className={styles.mark} aria-hidden="true">
              {marks[line.kind]}
            </span>
            <span className={styles.spoken}>{spoken[line.kind]}</span>
            <span className={styles.text}>{line.text}</span>
          </span>
        ))}
      </pre>
      {shown.clipped ? (
        <button
          type="button"
          className={styles.more}
          onClick={() => {
            if (revealKey === undefined) {
              setLocalAll(true)
            } else {
              environment.reveal(revealKey)
            }
          }}
        >
          {shown.byChars ? `Show all (${thousands(chars)} K characters)` : `Show all ${lines.length} lines`}
        </button>
      ) : null}
    </div>
  )
}

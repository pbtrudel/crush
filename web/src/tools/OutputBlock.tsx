import { clsx } from 'clsx'
import { useCallback, useContext, useId, useState, useSyncExternalStore, type ReactNode } from 'react'

import { ToolEnvironmentContext } from './environment'
import styles from './Tools.module.css'

// maxOutputChars bounds what an unrevealed OutputBlock renders. The line
// clip alone does not: a tool result can be one enormous line, and JSON
// escapes its newlines, so an argument can arrive as one too.
export const maxOutputChars = 20000

export interface OutputBlockProps {
  text: string
  maxLines?: number
  tone?: 'plain' | 'error'
  // revealKey names this block in the environment's revealed set, so the
  // choice survives the row unmounting. Without one the block keeps the
  // state itself.
  revealKey?: string
}

// splitLines counts a document's lines without letting a final newline add
// an empty one: a result of exactly maxLines lines and a newline is whole.
function splitLines(text: string): string[] {
  return (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n')
}

function kilobytes(length: number): number {
  return Math.ceil(length / 1024)
}

// OutputBlock is a tool's text output. A long one is clipped until the
// person asks for the rest, so one huge result cannot stall the log.
export default function OutputBlock({ text, maxLines = 40, tone = 'plain', revealKey }: OutputBlockProps) {
  const environment = useContext(ToolEnvironmentContext)
  const [localAll, setLocalAll] = useState(false)
  const readRevealed = useCallback(
    () => revealKey !== undefined && environment.revealed.has(revealKey),
    [environment, revealKey],
  )
  const revealed = useSyncExternalStore(environment.revealed.subscribe, readRevealed)
  const all = revealKey === undefined ? localAll : revealed
  const lines = splitLines(text)
  const charClipped = !all && text.length > maxOutputChars
  const lineClipped = !all && lines.length > maxLines
  const clipped = charClipped || lineClipped
  const shown = charClipped
    ? text.slice(0, maxOutputChars)
    : lineClipped
      ? lines.slice(0, maxLines).join('\n')
      : text
  return (
    <div className={styles.output}>
      <pre className={clsx(styles.pre, tone === 'error' && styles.errorText)}>{shown}</pre>
      {clipped ? (
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
          {charClipped ? `Show all (${kilobytes(text.length)} KB)` : `Show all ${lines.length} lines`}
        </button>
      ) : null}
    </div>
  )
}

// ToolSection groups a labelled part of a body. It is a named group, not a
// landmark: a row's Input and Output are two groups in the same list.
export function ToolSection({ label, children }: { label: string; children: ReactNode }) {
  const labelId = useId()
  return (
    <div className={styles.section} role="group" aria-labelledby={labelId}>
      <div className={styles.sectionLabel} id={labelId}>
        {label}
      </div>
      {children}
    </div>
  )
}

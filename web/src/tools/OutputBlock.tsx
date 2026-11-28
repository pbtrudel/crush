import { clsx } from 'clsx'
import { useCallback, useContext, useId, useState, useSyncExternalStore, type ReactNode } from 'react'

import { ToolEnvironmentContext } from './environment'
import styles from './Tools.module.css'

// maxOutputChars bounds what an unrevealed OutputBlock renders after the
// line clip. The line clip alone does not: a tool result can be one
// enormous line, and JSON escapes its newlines, so an argument can arrive
// as one too.
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

// clipChars cuts text to max UTF-16 code units. It drops a dangling high
// surrogate: half a pair renders as a replacement glyph.
function clipChars(text: string, max: number): string {
  let end = max
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return text.slice(0, end)
}

function thousands(length: number): number {
  return Math.ceil(length / 1000)
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
  // The line clip runs first: a long result of short lines is its first 40
  // lines, not the first 20000 characters of it, which would be hundreds of
  // lines.
  const lineClipped = !all && lines.length > maxLines
  const byLines = lineClipped ? lines.slice(0, maxLines).join('\n') : text
  const charClipped = !all && byLines.length > maxOutputChars
  const shown = charClipped ? clipChars(byLines, maxOutputChars) : byLines
  const clipped = lineClipped || charClipped
  // The label names the limit that cut the text. The character cap runs
  // last, so where both apply it is the one to say.
  const showAll = charClipped
    ? `Show all (${thousands(text.length)} K characters)`
    : `Show all ${lines.length} lines`
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
          {showAll}
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

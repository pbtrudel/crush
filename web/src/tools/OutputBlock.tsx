import { clsx } from 'clsx'
import { useId, type ReactNode } from 'react'

import { clipText, thousands } from './clip'
import { useReveal } from './reveal'
import styles from './Tools.module.css'

export interface OutputBlockProps {
  text: string
  maxLines?: number
  tone?: 'plain' | 'error'
  // revealKey names this block in the environment's revealed set, so the
  // choice survives the row unmounting. Without one the block keeps the
  // state itself.
  revealKey?: string
}

// OutputBlock is a tool's text output. A long one is clipped until the
// person asks for the rest, so one huge result cannot stall the log.
export default function OutputBlock({ text, maxLines = 40, tone = 'plain', revealKey }: OutputBlockProps) {
  const { all, reveal } = useReveal(revealKey)
  const clipped = clipText(text, maxLines)
  const shown = all ? text : clipped.text
  // The label names the limit that cut the text. The character cap runs
  // last, so where both apply it is the one to say.
  const showAll = clipped.charClipped
    ? `Show all (${thousands(text.length)} K characters)`
    : `Show all ${clipped.lines} lines`
  return (
    <div className={styles.output}>
      <pre className={clsx(styles.pre, tone === 'error' && styles.errorText)}>{shown}</pre>
      {!all && (clipped.lineClipped || clipped.charClipped) ? (
        <button type="button" className={styles.more} onClick={reveal}>
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

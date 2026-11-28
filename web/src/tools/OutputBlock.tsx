import { clsx } from 'clsx'
import { useState, type ReactNode } from 'react'

import styles from './Tools.module.css'

export interface OutputBlockProps {
  text: string
  maxLines?: number
  tone?: 'plain' | 'error'
}

// OutputBlock is a tool's text output. A long one is clipped until the
// person asks for the rest, so one huge result cannot stall the log.
export default function OutputBlock({ text, maxLines = 40, tone = 'plain' }: OutputBlockProps) {
  const [all, setAll] = useState(false)
  const lines = text.split('\n')
  const clipped = !all && lines.length > maxLines
  return (
    <div className={styles.output}>
      <pre className={clsx(styles.pre, tone === 'error' && styles.errorText)}>
        {clipped ? lines.slice(0, maxLines).join('\n') : text}
      </pre>
      {clipped ? (
        <button type="button" className={styles.more} onClick={() => setAll(true)}>
          Show all {lines.length} lines
        </button>
      ) : null}
    </div>
  )
}

export function ToolSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section className={styles.section} aria-label={label}>
      <div className={styles.sectionLabel}>{label}</div>
      {children}
    </section>
  )
}

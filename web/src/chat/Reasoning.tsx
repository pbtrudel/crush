import { formatDuration } from './format'
import styles from './MessageView.module.css'

export interface ReasoningProps {
  thinking: string
  startedAt: number
  finishedAt: number
  streaming: boolean
}

function preview(thinking: string): string {
  const line = thinking.trim().split('\n')[0] ?? ''
  return line.length > 120 ? `${line.slice(0, 119)}…` : line
}

// Reasoning is the model's thinking: a one-line preview that opens to the
// whole text. It is never Markdown, because it is not written for people.
export default function Reasoning({ thinking, startedAt, finishedAt, streaming }: ReasoningProps) {
  const label = streaming
    ? 'Thinking…'
    : startedAt > 0 && finishedAt > startedAt
      ? `Thought for ${formatDuration(finishedAt - startedAt)}`
      : 'Reasoning'
  return (
    <details className={styles.reasoning}>
      <summary className={styles.reasoningSummary}>
        <span className={styles.reasoningLabel}>{label}</span>
        <span className={styles.reasoningPreview}>{preview(thinking)}</span>
      </summary>
      <div className={styles.reasoningBody}>{thinking}</div>
    </details>
  )
}

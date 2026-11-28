import { firstLine } from '../tools/input'
import { formatDuration } from './format'
import styles from './MessageView.module.css'

export interface ReasoningProps {
  thinking: string
  startedAt: number
  finishedAt: number
  streaming: boolean
}

// Reasoning is the model's thinking: a one-line preview that opens to the
// whole text. It is never Markdown, because it is not written for people.
export default function Reasoning({ thinking, startedAt, finishedAt, streaming }: ReasoningProps) {
  // A part that carries its own finish is done thinking, whatever the turn
  // around it is still doing: the duration replaces the live label.
  const finished = startedAt > 0 && finishedAt > startedAt
  const label = finished
    ? `Thought for ${formatDuration(finishedAt - startedAt)}`
    : streaming
      ? 'Thinking…'
      : 'Reasoning'
  return (
    <details className={styles.reasoning}>
      <summary className={styles.reasoningSummary}>
        <span className={styles.reasoningLabel}>{label}</span>
        <span className={styles.reasoningPreview}>{firstLine(thinking, 120)}</span>
      </summary>
      <div className={styles.reasoningBody}>{thinking}</div>
    </details>
  )
}

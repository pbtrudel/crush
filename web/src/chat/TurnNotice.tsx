import { clsx } from 'clsx'

import type { FinishPart } from './parts'
import styles from './MessageView.module.css'

// A Map, so a reason the gateway may add later, or any odd string, finds
// nothing instead of an inherited property.
const notices = new Map<string, string>([
  ['error', 'The turn stopped with an error.'],
  ['canceled', 'Canceled.'],
  ['max_tokens', 'Stopped at the output token limit.'],
  ['content_filter', 'The provider declined to answer.'],
])

export function noticeFor(finish: FinishPart | null): string | null {
  return finish === null ? null : (notices.get(finish.reason) ?? null)
}

// TurnNotice ends a turn that did not end normally, with the provider's
// own message when it gave one.
export default function TurnNotice({ finish }: { finish: FinishPart | null }) {
  const text = noticeFor(finish)
  if (finish === null || text === null) {
    return null
  }
  return (
    <div className={clsx(styles.notice, finish.reason === 'error' && styles.noticeError)} role="note">
      {finish.message === '' ? text : `${text} ${finish.message}`}
      {finish.details !== '' ? (
        <details className={styles.noticeDetails}>
          <summary>Details</summary>
          <pre>{finish.details}</pre>
        </details>
      ) : null}
    </div>
  )
}

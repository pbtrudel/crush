import { clsx } from 'clsx'

import { connectionNotice, type AppState } from '../store/app'
import { useStore, type Store } from '../store/store'
import styles from './ConnectionStatus.module.css'

export interface ConnectionStatusProps {
  store: Store<AppState>
}

export const reconnectingText = 'Reconnecting…'
export const endedText = 'Crush has stopped. Run /web in Crush to reconnect.'

// ConnectionStatus is the connection banner the spec asks for: it names a
// reconnect in progress and says when Crush has stopped for good. The
// selector answers a string, so the row re-renders only when the sentence
// changes.
export default function ConnectionStatus({ store }: ConnectionStatusProps) {
  const notice = useStore(store, connectionNotice)
  if (notice === 'none') {
    return null
  }
  return (
    <p role="status" className={clsx(styles.status, notice === 'ended' && styles.ended)} data-state={notice}>
      {notice === 'reconnecting' ? reconnectingText : endedText}
    </p>
  )
}

import styles from './Unauthorized.module.css'

export interface UnreachableProps {
  message: string | null
}

// Unreachable is the screen a tab shows when the gateway does not answer at
// all. The token may be perfectly good, so it is kept for the next reload.
export default function Unreachable({ message }: UnreachableProps) {
  return (
    <main className={styles.screen}>
      <h1>Crush is not answering</h1>
      <p>The tab could not reach its gateway. Check the terminal is still running, then reload.</p>
      {message ? <p className={styles.detail}>{message}</p> : null}
    </main>
  )
}

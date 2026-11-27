import styles from './Unauthorized.module.css'

export interface UnreachableProps {
  message: string | null
}

// Unreachable is the screen a tab shows when the gateway does not answer at
// all. The launch token left the address bar as soon as it was read, so a
// reload cannot retry with it: the terminal has to hand out a new link.
export default function Unreachable({ message }: UnreachableProps) {
  return (
    <main className={styles.screen}>
      <h1>Crush is not answering</h1>
      <p>The tab could not reach its gateway. Check the terminal is still running, then reload.</p>
      <p>
        If the terminal is running, run <code>/web</code> in Crush to open a fresh link.
      </p>
      {message ? <p className={styles.detail}>{message}</p> : null}
    </main>
  )
}

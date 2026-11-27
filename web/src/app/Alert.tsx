import { visibleError, type AppState } from '../store/app'
import { useStore, type Store } from '../store/store'
import styles from './Alert.module.css'

export interface AlertProps {
  store: Store<AppState>
}

// Alert is the shell's inline row for the last web_error frame. The frame is
// global, so visibleError decides whether it belongs to the session on
// screen; the message is the gateway's own sentence and never a token.
export default function Alert({ store }: AlertProps) {
  const error = useStore(store, visibleError)
  if (error === null) {
    return null
  }
  return (
    <p role="alert" className={styles.alert} data-code={error.code}>
      {error.error}
    </p>
  )
}

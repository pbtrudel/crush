import { useRef } from 'react'

import { visibleError, type AppState, type AppStore } from '../store/app'
import { useStore, type Store } from '../store/store'
import styles from './Alert.module.css'

export interface AlertProps {
  store: Store<AppState> & AppStore
}

// Alert is the shell's inline row for the last web_error frame. The frame is
// global, so visibleError decides whether it belongs to the session on
// screen; the message is the gateway's own sentence and never a token. Only
// that sentence is the live region: the Dismiss button sits outside it, so a
// screen reader announces the error once instead of the button too.
export default function Alert({ store }: AlertProps) {
  const error = useStore(store, visibleError)
  const message = useRef<HTMLParagraphElement>(null)

  if (error === null) {
    return null
  }

  // The alert leaves with the click, so focus moves to the region it covered
  // first: the button is about to unmount, and focus would otherwise fall to
  // the body.
  const dismiss = () => {
    const region = message.current?.closest('main')
    if (region instanceof HTMLElement) {
      region.focus()
    }
    store.dismissError()
  }

  return (
    <div className={styles.row}>
      <p ref={message} role="alert" className={styles.alert} data-code={error.code}>
        {error.error}
      </p>
      <button type="button" className={styles.dismiss} onClick={dismiss}>
        Dismiss
      </button>
    </div>
  )
}

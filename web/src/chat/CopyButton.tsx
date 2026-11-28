import { useEffect, useState } from 'react'

import styles from './CopyButton.module.css'

export interface CopyButtonProps {
  text: string
  label: string
}

type CopyState = 'idle' | 'copied' | 'failed'

const captions: Record<CopyState, string> = { idle: 'Copy', copied: 'Copied', failed: 'Copy failed' }

// CopyButton writes its text to the clipboard and says what came of it for
// a moment: a denied clipboard says so rather than looking like a no-op.
export default function CopyButton({ text, label }: CopyButtonProps) {
  const [state, setState] = useState<CopyState>('idle')
  useEffect(() => {
    if (state === 'idle') {
      return
    }
    const timer = window.setTimeout(() => setState('idle'), 1500)
    return () => window.clearTimeout(timer)
  }, [state])
  return (
    <>
      <button
        type="button"
        className={styles.copy}
        aria-label={label}
        onClick={() => {
          void navigator.clipboard?.writeText(text).then(
            () => setState('copied'),
            () => setState('failed'),
          )
        }}
      >
        {captions[state]}
      </button>
      {/* The button keeps its contextual name, which would hide the visible
       * result from a screen reader. The polite region carries the result
       * instead; it is empty until there is one to announce. */}
      <span className={styles.status} role="status">
        {state === 'idle' ? '' : captions[state]}
      </span>
    </>
  )
}

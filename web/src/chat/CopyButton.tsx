import { useEffect, useState } from 'react'

import styles from './CopyButton.module.css'

export interface CopyButtonProps {
  text: string
  label: string
}

// CopyButton writes its text to the clipboard and says so for a moment.
export default function CopyButton({ text, label }: CopyButtonProps) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) {
      return
    }
    const timer = window.setTimeout(() => setCopied(false), 1500)
    return () => window.clearTimeout(timer)
  }, [copied])
  return (
    <button
      type="button"
      className={styles.copy}
      aria-label={label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        )
      }}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

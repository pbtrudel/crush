import { useEffect, useRef } from 'react'

import type { WireMessage } from '../store/app'
import MessageView from './MessageView'
import styles from './Conversation.module.css'

export interface ConversationProps {
  sessionId: string | null
  title: string
  messages: WireMessage[]
  hasMore: boolean
  loadingOlder: boolean
  error?: string | null
  onLoadOlder(): void
}

// Conversation is the read-only chat: the session's title, one message view
// per message and, while the gateway says there is history before the first
// page, the button that asks for it.
export default function Conversation({
  sessionId,
  title,
  messages,
  hasMore,
  loadingOlder,
  error = null,
  onLoadOlder,
}: ConversationProps) {
  const bottom = useRef<HTMLDivElement>(null)

  // The log scrolls to its end as messages arrive. scrollIntoView is optional
  // because jsdom has no layout and does not implement it.
  useEffect(() => {
    bottom.current?.scrollIntoView?.({ block: 'end' })
  }, [messages.length, sessionId])

  if (sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title}</h1>
      </header>
      <div className={styles.log}>
        {hasMore ? (
          <button
            type="button"
            className={styles.older}
            onClick={onLoadOlder}
            disabled={loadingOlder}
          >
            {loadingOlder ? 'Loading…' : 'Load older messages'}
          </button>
        ) : null}
        {error ? (
          <p role="alert" className={styles.error}>
            {error}
          </p>
        ) : null}
        {messages.map((message) => (
          <MessageView key={message.id} message={message} />
        ))}
        <div ref={bottom} />
      </div>
    </section>
  )
}

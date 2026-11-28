import { useEffect, useRef } from 'react'

import type { SessionStatus } from '../api/wire'
import MessageView from '../chat/MessageView'
import { isTurnActive } from '../chat/toolStatus'
import { useResults } from '../chat/useResults'
import type { WireMessage } from '../store/app'
import styles from './Conversation.module.css'

export interface ConversationProps {
  sessionId: string | null
  title: string
  messages: WireMessage[]
  hasMore: boolean
  loadingOlder: boolean
  error?: string | null
  // status is the session row's run state; it decides which turn is live.
  status?: SessionStatus
  onLoadOlder(): void
}

// followDistance is how close to the end the log counts as "at the end". A
// few pixels of slack keep a fractional device pixel from unpinning it.
const followDistance = 24

// untitled is what a session with no row or no title is called, so the header
// is never blank.
const untitled = 'Untitled session'

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
  status,
  onLoadOlder,
}: ConversationProps) {
  const log = useRef<HTMLDivElement>(null)
  const bottom = useRef<HTMLDivElement>(null)
  // pinned is whether the log sat at its end before the last change: a new
  // message follows the conversation only while the person is already there.
  const pinned = useRef(true)
  const seen = useRef<{ sessionId: string | null; newest: string | null } | null>(null)
  const newest = messages.length === 0 ? null : messages[messages.length - 1].id

  // Tool results live in their own messages, so the calls that answered are
  // collected from the whole log. The index keeps its identity while no
  // result changes, so a streamed token re-renders the last message only.
  const results = useResults(messages)
  // Only the turn after the last user message can still be running; a call
  // left open in an earlier turn is interrupted, whatever the session does.
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  const live = isTurnActive(status)

  // A session opens at its end, and a message that arrives while the log is
  // at its end is followed. An older page changes neither the session nor the
  // newest message, so loading one never moves the view.
  useEffect(() => {
    const previous = seen.current
    seen.current = { sessionId, newest }
    if (previous === null || previous.sessionId !== sessionId) {
      pinned.current = true
      bottom.current?.scrollIntoView?.({ block: 'end' })
      return
    }
    if (previous.newest === newest || !pinned.current) {
      return
    }
    bottom.current?.scrollIntoView?.({ block: 'end' })
  }, [sessionId, newest])

  // The scroll position is read here because the browser is the only one
  // that knows it: by the time a new message is in the DOM it is too late.
  const trackScroll = () => {
    const element = log.current
    if (element === null) {
      return
    }
    pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight <= followDistance
  }

  if (sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title === '' ? untitled : title}</h1>
      </header>
      <div ref={log} className={styles.log} data-testid="conversation-log" onScroll={trackScroll}>
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
        {messages.map((message, index) => (
          <MessageView
            key={message.id}
            message={message}
            results={results}
            turnActive={live && index > lastUser}
          />
        ))}
        <div ref={bottom} />
      </div>
    </section>
  )
}

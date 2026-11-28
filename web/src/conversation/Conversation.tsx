import { useEffect, useMemo, useRef } from 'react'

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
  // collected from the whole log. The set is rebuilt only when an answer
  // changes: a token in the last message makes a new array, and a new set
  // with it would re-render every view that reads one.
  const answeredIDs = useMemo(() => {
    const answered = new Set<string>()
    for (const entry of messages) {
      for (const part of entry.parts) {
        if (part.type !== 'tool_result') {
          continue
        }
        const result = part.data as { tool_call_id?: unknown }
        if (typeof result.tool_call_id === 'string') {
          answered.add(result.tool_call_id)
        }
      }
    }
    return [...answered].join(',')
  }, [messages])
  const toolResults = useMemo(
    () => new Set(answeredIDs === '' ? [] : answeredIDs.split(',')),
    [answeredIDs],
  )

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
        {messages.map((message) => (
          <MessageView key={message.id} message={message} toolResults={toolResults} />
        ))}
        <div ref={bottom} />
      </div>
    </section>
  )
}

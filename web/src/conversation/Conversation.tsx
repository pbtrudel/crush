import { useVirtualizer } from '@tanstack/react-virtual'
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'

import type { SessionStatus } from '../api/wire'
import MessageView from '../chat/MessageView'
import { isTurnActive } from '../chat/toolStatus'
import { turnFooters } from '../chat/turns'
import { useResults } from '../chat/useResults'
import UsageSummary, { type Usage } from '../chat/UsageSummary'
import type { WireMessage } from '../store/app'
import { anchoredScrollTop, isAtEnd, prependedCount } from './scroll'
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
  // usage is the session's context and cost, for the header.
  usage?: Usage | null
  onLoadOlder(): void
}

const untitled = 'Untitled session'

// edgeKey is row 0, the history edge, so a message id never collides with
// it.
const edgeKey = 'history-edge'

// estimatedRowHeight is a guess for a row not measured yet. Only rows near
// the view are ever measured, so the spacer's height is partly a guess.
const estimatedRowHeight = 120

// Shape is what the last commit left: the row keys, the scroll top and
// where the first message started, which is what a prepend is measured
// against.
interface Shape {
  keys: string[]
  scrollTop: number
  firstStart: number
}

export default function Conversation(props: ConversationProps) {
  if (props.sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }
  // A new session is a new log: it opens at its end with fresh state.
  return <Log key={props.sessionId} {...props} />
}

interface HistoryEdgeProps {
  hasMore: boolean
  loadingOlder: boolean
  error: string | null
  onLoadOlder(): void
}

function HistoryEdge({ hasMore, loadingOlder, error, onLoadOlder }: HistoryEdgeProps) {
  return (
    <div className={styles.edge}>
      {hasMore ? (
        <button type="button" className={styles.older} onClick={onLoadOlder} disabled={loadingOlder}>
          {loadingOlder ? 'Loading…' : 'Load older messages'}
        </button>
      ) : (
        <p className={styles.start}>Start of the session</p>
      )}
      {error ? (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      ) : null}
    </div>
  )
}

function Log({
  title,
  messages,
  hasMore,
  loadingOlder,
  error = null,
  status,
  usage = null,
  onLoadOlder,
}: ConversationProps) {
  const log = useRef<HTMLDivElement>(null)
  // pinned is whether the reader sits at the end. It is a ref because the
  // commit reads it, and a state would lag one render behind the scroll.
  const pinned = useRef(true)
  const shape = useRef<Shape | null>(null)
  const [away, setAway] = useState(false)

  // The index is rebuilt only when a result changes, so a streamed token
  // renders the message it lands in and no other.
  const results = useResults(messages)
  // One footer per assistant message that ends a turn, rebuilt only when
  // the log changes: every settled message gets the same string it had.
  const footers = useMemo(() => turnFooters(messages), [messages])
  const keys = useMemo(() => messages.map((entry) => entry.id), [messages])
  // Only the turn after the last user message can still be running.
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  const live = isTurnActive(status)

  const virtualizer = useVirtualizer({
    count: messages.length + 1,
    getScrollElement: () => log.current,
    estimateSize: () => estimatedRowHeight,
    overscan: 6,
    getItemKey: (index) => (index === 0 ? edgeKey : (keys[index - 1] ?? index)),
    // A measurement re-renders on the next frame instead of inside the
    // commit that measured it, which React warns about.
    useFlushSync: false,
  })

  // Every commit: follow the end while pinned, or keep the old first
  // message in place after a prepend. Reading the total size first
  // rebuilds the measurements, so the start below includes every row this
  // commit measured.
  useLayoutEffect(() => {
    const element = log.current
    if (element === null) {
      return
    }
    virtualizer.getTotalSize()
    const previous = shape.current
    const added = previous === null ? 0 : prependedCount(previous.keys, keys)
    if (previous === null || pinned.current) {
      element.scrollTop = element.scrollHeight
    } else if (added > 0) {
      const start = virtualizer.measurementsCache[added + 1]?.start
      if (start !== undefined) {
        element.scrollTop = anchoredScrollTop(previous.scrollTop, previous.firstStart, start)
      }
    }
    shape.current = {
      keys,
      scrollTop: element.scrollTop,
      firstStart: virtualizer.measurementsCache[1]?.start ?? 0,
    }
  })

  const trackScroll = useCallback(() => {
    const element = log.current
    if (element === null) {
      return
    }
    const atEnd = isAtEnd(element)
    pinned.current = atEnd
    setAway(!atEnd)
    if (shape.current !== null) {
      shape.current.scrollTop = element.scrollTop
    }
  }, [])

  const jumpToEnd = useCallback(() => {
    const element = log.current
    if (element === null) {
      return
    }
    pinned.current = true
    setAway(false)
    element.scrollTop = element.scrollHeight
  }, [])

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title === '' ? untitled : title}</h1>
        {usage !== null ? <UsageSummary usage={usage} /> : null}
      </header>
      <div className={styles.body}>
        <div
          ref={log}
          className={styles.log}
          data-testid="conversation-log"
          role="log"
          aria-label="Conversation log"
          // The log is a reading surface: announcing every streamed delta
          // would talk over the reader instead of saying something new.
          aria-live="off"
          onScroll={trackScroll}
        >
          {/* The spacer's height and each row's offset are geometry the
              virtualizer computes; React writes them through the CSSOM. */}
          <div className={styles.spacer} style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const message = item.index === 0 ? undefined : messages[item.index - 1]
              return (
                <div
                  key={item.key}
                  ref={virtualizer.measureElement}
                  data-index={item.index}
                  className={styles.row}
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  {message === undefined ? (
                    <HistoryEdge
                      hasMore={hasMore}
                      loadingOlder={loadingOlder}
                      error={error}
                      onLoadOlder={onLoadOlder}
                    />
                  ) : (
                    <MessageView
                      message={message}
                      results={results}
                      turnActive={live && item.index - 1 > lastUser}
                      footer={footers.get(message.id) ?? null}
                    />
                  )}
                </div>
              )
            })}
          </div>
        </div>
        {away ? (
          <button type="button" className={styles.toEnd} onClick={jumpToEnd}>
            Back to bottom
          </button>
        ) : null}
      </div>
    </section>
  )
}

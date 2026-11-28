import { defaultRangeExtractor, useVirtualizer, type Range } from '@tanstack/react-virtual'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import type { SessionStatus } from '../api/wire'
import MessageView, { drawsNothing } from '../chat/MessageView'
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

// focusedIndex is the row index of the element that holds the focus inside
// the log, or null when the focus is elsewhere or on the log itself. The
// row's key is read from the live DOM and resolved against the current
// keys, so an appended row, a prepended page or a filtered message cannot
// leave the index stale.
function focusedIndex(element: HTMLElement | null, keys: readonly string[]): number | null {
  if (element === null) {
    return null
  }
  const active = document.activeElement
  if (!(active instanceof HTMLElement) || !element.contains(active)) {
    return null
  }
  const row = active.closest<HTMLElement>('[data-key]')
  if (row === null || !element.contains(row)) {
    return null
  }
  const key = row.dataset.key
  if (key === undefined) {
    return null
  }
  if (key === edgeKey) {
    return 0
  }
  const index = keys.indexOf(key)
  return index === -1 ? null : index + 1
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
  // the log changes: every settled message gets the same string it had. A
  // message the view does not draw cannot carry its turn's footer, so the
  // footer moves to the last earlier assistant message that draws.
  const footers = useMemo(
    () => turnFooters(messages, (message) => !drawsNothing(message)),
    [messages],
  )
  // Only messages that draw something become rows: results belong to their
  // call, hidden text and a plain finish draw nothing, and an empty row
  // would still cost eight pixels and a message's height estimate.
  const drawn = useMemo(() => messages.filter((entry) => !drawsNothing(entry)), [messages])
  const keys = useMemo(() => drawn.map((entry) => entry.id), [drawn])
  // keysRef is the latest row keys for the range extractor, which reads the
  // focused row's key from the DOM and finds where that row is now.
  const keysRef = useRef(keys)
  keysRef.current = keys
  // Only the turn after the last user message can still be running.
  const lastUser = drawn.findLastIndex((entry) => entry.role === 'user')
  const live = isTurnActive(status)

  const rangeExtractor = useCallback(
    (range: Range) => {
      const indexes = defaultRangeExtractor(range)
      const focused = focusedIndex(log.current, keysRef.current)
      if (focused === null || focused < 0 || focused >= range.count || indexes.includes(focused)) {
        return indexes
      }
      // Sorted and unique: the virtualizer reads rows back by position.
      const merged = [...indexes, focused]
      merged.sort((a, b) => a - b)
      return merged
    },
    // The extractor reads the ref, but a new row list must re-run it even
    // when the row count happens to stay the same.
    [keys],
  )

  const virtualizer = useVirtualizer({
    count: drawn.length + 1,
    getScrollElement: () => log.current,
    estimateSize: () => estimatedRowHeight,
    overscan: 6,
    getItemKey: (index) => (index === 0 ? edgeKey : (keys[index - 1] ?? index)),
    rangeExtractor,
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

  // A streamed delta lands in a row's own memoised view, which does not
  // re-render this component: the layout effect above cannot see the row
  // grow. The observer reads the mutation before the browser paints, so a
  // pinned log follows a child-only tick too. A reader who scrolled up
  // leaves pinned false and the view alone.
  useEffect(() => {
    const element = log.current
    if (element === null) {
      return
    }
    const observer = new MutationObserver(() => {
      if (pinned.current) {
        element.scrollTop = element.scrollHeight
      }
    })
    observer.observe(element, { characterData: true, childList: true, subtree: true })
    return () => observer.disconnect()
  }, [])

  const jumpToEnd = useCallback(() => {
    const element = log.current
    if (element === null) {
      return
    }
    // The button goes away once the view is back at the end; take the
    // focus first, so it lands on the log instead of the body.
    element.focus({ preventScroll: true })
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
          // Programmatically focusable, so Back to bottom can hand it the
          // focus before the button disappears.
          tabIndex={-1}
          onScroll={trackScroll}
        >
          {/* The spacer's height and each row's offset are geometry the
              virtualizer computes; React writes them through the CSSOM. */}
          <div className={styles.spacer} style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const message = item.index === 0 ? undefined : drawn[item.index - 1]
              return (
                <div
                  key={item.key}
                  ref={virtualizer.measureElement}
                  data-index={item.index}
                  data-key={item.key}
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

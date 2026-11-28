import { clsx } from 'clsx'
import { memo, type ReactNode } from 'react'

import type { WireMessage } from '../api/wire'
import ToolCall from '../tools/ToolCall'
import CopyButton from './CopyButton'
import { formatTokens } from './format'
import Markdown from './markdown/Markdown'
import { finishOf, messageParts, type FinishPart, type ToolResultPart } from './parts'
import Reasoning from './Reasoning'
import { segments, type Segment } from './segments'
import { toolStatus } from './toolStatus'
import TurnNotice, { noticeFor } from './TurnNotice'
import { useStreamedSource } from './useStreamedSource'
import styles from './MessageView.module.css'

export interface MessageViewProps {
  message: WireMessage
  results: ReadonlyMap<string, ToolResultPart>
  // turnActive says the session may still be writing this message's turn:
  // it runs, and no user message came after this one.
  turnActive: boolean
  // depth is how deep in a subagent tree the message sits; 0 is the log.
  depth?: number
}

interface DrawContext {
  message: WireMessage
  results: ReadonlyMap<string, ToolResultPart>
  finish: FinishPart | null
  turnActive: boolean
  streaming: boolean
  depth: number
  last: boolean
}

// AssistantText is the model's prose. A settled block answers its source
// unchanged, and Markdown's own memo keeps it from parsing again; the block
// the turn is still writing is handed over once per pass, so a delta every
// few milliseconds does not parse the whole reply again.
function AssistantText({ source, streaming }: { source: string; streaming: boolean }) {
  return <Markdown source={useStreamedSource(source, streaming)} />
}

function draw(segment: Segment, context: DrawContext): ReactNode {
  if (segment.kind === 'tools') {
    return (
      <ul className={styles.tools}>
        {segment.calls.map((call) => {
          const result = context.results.get(call.id)
          return (
            <ToolCall
              key={call.id}
              call={call}
              result={result}
              status={toolStatus(result, context.finish, context.turnActive)}
              messageId={context.message.id}
              depth={context.depth}
            />
          )
        })}
      </ul>
    )
  }
  const part = segment.part
  switch (part.kind) {
    case 'text':
      return context.message.role === 'user' ? (
        <p className={styles.userText}>{part.text}</p>
      ) : (
        <AssistantText source={part.text} streaming={context.streaming && context.last} />
      )
    case 'reasoning':
      return (
        <Reasoning
          thinking={part.thinking}
          startedAt={part.startedAt}
          finishedAt={part.finishedAt}
          streaming={context.streaming && context.last}
        />
      )
    case 'shell_command':
      return (
        <pre className={styles.shell}>
          {`$ ${part.command}\n${part.output}`}
          {part.exitCode !== 0 ? `\nexit ${part.exitCode}` : ''}
        </pre>
      )
    case 'sub_agent_report':
      return (
        <details className={styles.report}>
          <summary>
            {part.label}
            {part.failed ? ' (failed)' : ''}
            {part.model !== '' ? ` · ${part.model}` : ''}
          </summary>
          <Markdown source={part.output} />
        </details>
      )
    case 'compaction':
      return (
        <p className={styles.note}>
          Compacted the conversation, saving {formatTokens(part.savedTokens)} tokens.
        </p>
      )
    case 'attachment':
      return <span className={styles.attachment}>{part.name}</span>
  }
}

// MessageView draws one message, its parts in the order they were written.
// It is memoised: a streamed token makes a new object for the last message
// only, and the result index keeps its identity until a result changes.
function MessageView({ message, results, turnActive, depth = 0 }: MessageViewProps) {
  const parts = messageParts(message)
  const finish = finishOf(parts)
  const list = segments(parts)
  if (list.length === 0 && noticeFor(finish) === null) {
    return null
  }
  const user = message.role === 'user'
  const streaming = !user && turnActive && finish === null
  const copyText = parts
    .flatMap((part) => (part.kind === 'text' ? [part.text] : []))
    .join('\n\n')
  return (
    <article
      className={clsx(styles.message, user ? styles.user : styles.assistant, streaming && styles.streaming)}
      aria-busy={streaming ? true : undefined}
      data-role={message.role}
    >
      {list.map((segment, index) => (
        <div key={index} className={styles.segment}>
          {draw(segment, {
            message,
            results,
            finish,
            turnActive,
            streaming,
            depth,
            last: index === list.length - 1,
          })}
        </div>
      ))}
      <TurnNotice finish={finish} />
      {!user && !streaming && depth === 0 && copyText !== '' ? (
        <div className={styles.actions}>
          <CopyButton text={copyText} label="Copy message" />
        </div>
      ) : null}
    </article>
  )
}

export default memo(MessageView)

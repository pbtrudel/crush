import { clsx } from 'clsx'
import { memo } from 'react'

import type { WireMessage } from '../store/app'
import ToolCallRow from './ToolCallRow'
import styles from './MessageView.module.css'

export interface MessageViewProps {
  message: WireMessage
  // toolResults names the tool calls that have answered somewhere in the
  // log. A call's own `finished` flag only says its input stopped streaming,
  // so a result is what makes it done.
  toolResults?: ReadonlySet<string>
}

interface TextPart {
  text: string
  hidden?: boolean
}

interface ReasoningPart {
  thinking: string
}

interface ToolCallPart {
  id: string
  name: string
}

// noToolResults is the one empty set: a fresh one on every render would be a
// new prop and undo the memo.
const noToolResults: ReadonlySet<string> = new Set<string>()

// MessageView renders one message as plain text, one collapsed block per
// reasoning part and one row per tool call. Markdown and the tool views
// arrive in M2: what is here is what the wire carries, nothing invented.
// Every part is rendered through React, so text stays text. It is memoised
// because the conversation changes on every streamed token: a message whose
// own object and result set are unchanged has nothing new to draw.
function MessageView({ message, toolResults = noToolResults }: MessageViewProps) {
  const texts: string[] = []
  const reasonings: string[] = []
  const calls: ToolCallPart[] = []
  for (const part of message.parts) {
    if (part.type === 'text') {
      const content = part.data as TextPart
      // Hidden text is model history only — the environment snapshot, the
      // reminders, the compaction state — and the TUI drops it too.
      if (content.hidden === true || typeof content.text !== 'string') {
        continue
      }
      texts.push(content.text)
    } else if (part.type === 'reasoning') {
      const content = part.data as ReasoningPart
      if (typeof content.thinking !== 'string') {
        continue
      }
      reasonings.push(content.thinking)
    } else if (part.type === 'tool_call') {
      const content = part.data as ToolCallPart
      if (typeof content.id !== 'string' || typeof content.name !== 'string') {
        continue
      }
      calls.push(content)
    }
  }

  // A tool result, a shell command and the other model-only parts carry
  // nothing this view draws, so a message made of them is not on screen.
  if (texts.length === 0 && reasonings.length === 0 && calls.length === 0) {
    return null
  }

  return (
    <article className={clsx(styles.message, message.role === 'user' ? styles.user : styles.assistant)}>
      {reasonings.map((thinking, index) => (
        <details key={`reasoning-${index}`} className={styles.reasoning}>
          <summary>Reasoning</summary>
          <pre>{thinking}</pre>
        </details>
      ))}
      {texts.map((value, index) => (
        <p key={`text-${index}`} className={styles.text}>
          {value}
        </p>
      ))}
      {calls.length > 0 ? (
        <ul className={styles.tools}>
          {calls.map((call) => (
            <ToolCallRow key={call.id} name={call.name} done={toolResults.has(call.id)} />
          ))}
        </ul>
      ) : null}
    </article>
  )
}

export default memo(MessageView)

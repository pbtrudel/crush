import { clsx } from 'clsx'

import type { WireMessage } from '../store/app'
import ToolCallRow from './ToolCallRow'
import styles from './MessageView.module.css'

export interface MessageViewProps {
  message: WireMessage
}

interface TextPart {
  text: string
}

interface ReasoningPart {
  thinking: string
}

interface ToolCallPart {
  id: string
  name: string
  finished?: boolean
}

// MessageView renders one message as plain text, one collapsed block per
// reasoning part and one row per tool call. Markdown and the tool views
// arrive in M2: what is here is what the wire carries, nothing invented.
// Every part is rendered through React, so text stays text.
export default function MessageView({ message }: MessageViewProps) {
  const texts: string[] = []
  const reasonings: string[] = []
  const calls: ToolCallPart[] = []
  for (const part of message.parts) {
    if (part.type === 'text') {
      texts.push((part.data as TextPart).text)
    } else if (part.type === 'reasoning') {
      reasonings.push((part.data as ReasoningPart).thinking)
    } else if (part.type === 'tool_call') {
      calls.push(part.data as ToolCallPart)
    }
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
            <ToolCallRow key={call.id} name={call.name} finished={call.finished === true} />
          ))}
        </ul>
      ) : null}
    </article>
  )
}

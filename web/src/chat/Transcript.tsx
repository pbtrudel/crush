import type { ReactNode } from 'react'

import type { WireMessage } from '../api/wire'
import type { RenderTranscript } from '../tools/environment'
import MessageView from './MessageView'
import { useResults } from './useResults'

export interface TranscriptProps {
  messages: WireMessage[]
  // depth is the level the messages sit at: 0 is the log.
  depth: number
  // turnActive says the session may still be writing its last turn.
  turnActive: boolean
}

// Transcript draws a session's messages with the log's own message view. It
// is a component of its own so the result index comes from the one hook that
// memoises it: a streamed delta then re-renders the last message only, the
// same way the log behaves.
function Transcript({ messages, depth, turnActive }: TranscriptProps) {
  const results = useResults(messages)
  // Only the turn after the last user message can still be running; a call
  // left open in an earlier turn is interrupted, whatever the session does.
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  return (
    <>
      {messages.map((message, index) => (
        <MessageView
          key={message.id}
          message={message}
          results={results}
          turnActive={turnActive && index > lastUser}
          depth={depth}
        />
      ))}
    </>
  )
}

// renderTranscript is handed to the tool views through the environment, so a
// subagent's messages draw with the same view as the log's. It is a module
// constant: the tool environment rebuilds when its props change, and a fresh
// function on every render would re-render every row in the log.
export const renderTranscript: RenderTranscript = (messages, depth, turnActive): ReactNode => (
  <Transcript messages={messages} depth={depth} turnActive={turnActive} />
)

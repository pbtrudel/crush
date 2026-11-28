import type { WireMessage } from '../api/wire'
import { formatDuration } from './format'
import { finishOf, messageParts } from './parts'

// turnFooters writes the footer of every assistant message that ends a
// turn: one whose finish is anything but tool_use. It reads "model ·
// duration", the duration running from the turn's user message to the
// finish, in the seconds the gateway stamps. A footer is a string so a
// memoised message view compares it by value, and the whole map is
// computed once per message list, so a streamed token costs one pass and
// leaves every settled footer the string it already had.
//
// A turn whose user message is on a page the tab has not loaded is timed
// from the first message the log does carry: the footer says how long the
// step the reader can see took. A turn still running has no finish, and so
// no footer.
export function turnFooters(messages: WireMessage[]): Map<string, string> {
  const out = new Map<string, string>()
  let turnStart: number | null = null
  for (const message of messages) {
    if (message.role === 'user') {
      turnStart = message.created_at
      continue
    }
    if (message.role !== 'assistant') {
      continue
    }
    const finish = finishOf(messageParts(message))
    if (finish === null || finish.reason === 'tool_use') {
      continue
    }
    const started = turnStart ?? message.created_at
    const pieces: string[] = []
    if (message.model) {
      pieces.push(message.model)
    }
    const elapsed = finish.time - started
    if (Number.isFinite(elapsed) && finish.time > 0 && elapsed >= 0) {
      pieces.push(formatDuration(elapsed))
    }
    if (pieces.length > 0) {
      out.set(message.id, pieces.join(' · '))
    }
  }
  return out
}

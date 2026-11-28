import type { WireMessage } from '../api/wire'
import { formatDuration } from './format'
import { finishOf, messageParts } from './parts'

// turnFooters writes the footer of every assistant message that ends a
// turn: one whose finish is anything but tool_use, and that is not a
// compaction summary. It reads "model · duration", the duration running
// from the turn's user message to the finish, in the seconds the gateway
// stamps. A footer is a string so a memoised message view compares it by
// value, and the whole map is computed once per message list, so a
// streamed token costs one pass and leaves every settled footer the string
// it already had.
//
// The model is the display name the gateway sends beside the id when it has
// one, and the id otherwise. The duration is left out when the prompt is
// unknown, which is the case for a turn whose user message sits on a page
// the tab has not loaded: the footer names the model alone rather than
// timing the step the reader can see. A turn still running has no finish,
// and so no footer. Compaction summaries draw none either, the way the TUI
// suppresses them (internal/ui/chat/messages.go ShouldShowAssistantInfo).
export function turnFooters(messages: WireMessage[]): Map<string, string> {
  const out = new Map<string, string>()
  let turnStart: number | null = null
  for (const message of messages) {
    if (message.role === 'user') {
      turnStart = message.created_at
      continue
    }
    if (message.role !== 'assistant' || message.is_summary_message === true) {
      continue
    }
    const finish = finishOf(messageParts(message))
    if (finish === null || finish.reason === 'tool_use') {
      continue
    }
    const pieces: string[] = []
    const model = message.model_name || message.model
    if (model) {
      pieces.push(model)
    }
    if (turnStart !== null) {
      const elapsed = finish.time - turnStart
      if (Number.isFinite(elapsed) && finish.time > 0 && elapsed >= 0) {
        pieces.push(formatDuration(elapsed))
      }
    }
    if (pieces.length > 0) {
      out.set(message.id, pieces.join(' · '))
    }
  }
  return out
}

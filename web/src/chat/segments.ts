import type { MessagePart, ToolCallPart } from './parts'

export type DrawnPart = Exclude<MessagePart, { kind: 'tool_call' | 'tool_result' | 'finish' }>

// Segment is one block of a message, in the order it was written: a run of
// consecutive tool calls is one tool group, everything else stands alone.
export type Segment = { kind: 'tools'; calls: ToolCallPart[] } | { kind: 'part'; part: DrawnPart }

// segments drops what the message does not draw itself: results belong to
// their calls, and the finish part is the notice under the message.
export function segments(parts: MessagePart[]): Segment[] {
  const out: Segment[] = []
  for (const part of parts) {
    if (part.kind === 'tool_result' || part.kind === 'finish') {
      continue
    }
    if (part.kind === 'tool_call') {
      const last = out[out.length - 1]
      if (last !== undefined && last.kind === 'tools') {
        last.calls.push(part.call)
      } else {
        out.push({ kind: 'tools', calls: [part.call] })
      }
      continue
    }
    out.push({ kind: 'part', part })
  }
  return out
}

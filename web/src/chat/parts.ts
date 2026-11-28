import { z } from 'zod'

import type { WireMessage } from '../api/wire'

// The part schemas pin the fields the chat reads. A part that fails its
// schema is dropped, not thrown: one malformed part must not hide the rest
// of the message, and the frame around it already passed the wire boundary.
const textSchema = z.object({ text: z.string(), hidden: z.boolean().optional() }).passthrough()
const reasoningSchema = z
  .object({
    thinking: z.string(),
    started_at: z.number().optional().default(0),
    finished_at: z.number().optional().default(0),
  })
  .passthrough()
const toolCallSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    input: z.string().optional().default(''),
    finished: z.boolean().optional().default(false),
  })
  .passthrough()
const toolResultSchema = z
  .object({
    tool_call_id: z.string(),
    name: z.string().optional().default(''),
    content: z.string().optional().default(''),
    mime_type: z.string().optional().default(''),
    metadata: z.string().optional().default(''),
    is_error: z.boolean().optional().default(false),
  })
  .passthrough()
const finishSchema = z
  .object({
    reason: z.string(),
    time: z.number().optional().default(0),
    message: z.string().optional().default(''),
    details: z.string().optional().default(''),
  })
  .passthrough()
const shellCommandSchema = z
  .object({ command: z.string(), output: z.string().optional().default(''), exit_code: z.number().optional().default(0) })
  .passthrough()
const subAgentReportSchema = z
  .object({
    label: z.string(),
    output: z.string().optional().default(''),
    failed: z.boolean().optional().default(false),
    model: z.string().optional().default(''),
  })
  .passthrough()
const compactionSchema = z
  .object({ saved_tokens: z.number(), kept_messages: z.number().optional().default(0) })
  .passthrough()
// BinaryContent has no JSON tags on the Go side, so its fields arrive
// capitalised. The gateway strips Data before the frame leaves.
const binarySchema = z
  .object({ Path: z.string().optional().default(''), MIMEType: z.string().optional().default('') })
  .passthrough()
const imageUrlSchema = z.object({ url: z.string() }).passthrough()

export interface ToolCallPart {
  id: string
  name: string
  input: string
  finished: boolean
}

export interface ToolResultPart {
  toolCallId: string
  name: string
  content: string
  mimeType: string
  metadata: string
  isError: boolean
}

export interface FinishPart {
  reason: string
  time: number
  message: string
  details: string
}

export type MessagePart =
  | { kind: 'text'; text: string }
  | { kind: 'reasoning'; thinking: string; startedAt: number; finishedAt: number }
  | { kind: 'tool_call'; call: ToolCallPart }
  | { kind: 'tool_result'; result: ToolResultPart }
  | { kind: 'finish'; finish: FinishPart }
  | { kind: 'shell_command'; command: string; output: string; exitCode: number }
  | { kind: 'sub_agent_report'; label: string; output: string; failed: boolean; model: string }
  | { kind: 'compaction'; savedTokens: number; keptMessages: number }
  | { kind: 'attachment'; name: string; mimeType: string }

// parsePart reads one wire part. It answers null for a type the chat does
// not draw, for hidden text (model history only: the environment snapshot,
// the reminders), and for a part whose fields do not parse.
export function parsePart(type: string, data: unknown): MessagePart | null {
  switch (type) {
    case 'text': {
      const parsed = textSchema.safeParse(data)
      if (!parsed.success || parsed.data.hidden === true) {
        return null
      }
      return { kind: 'text', text: parsed.data.text }
    }
    case 'reasoning': {
      const parsed = reasoningSchema.safeParse(data)
      if (!parsed.success || parsed.data.thinking === '') {
        return null
      }
      return {
        kind: 'reasoning',
        thinking: parsed.data.thinking,
        startedAt: parsed.data.started_at,
        finishedAt: parsed.data.finished_at,
      }
    }
    case 'tool_call': {
      const parsed = toolCallSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { id, name, input, finished } = parsed.data
      return { kind: 'tool_call', call: { id, name, input, finished } }
    }
    case 'tool_result': {
      const parsed = toolResultSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const value = parsed.data
      return {
        kind: 'tool_result',
        result: {
          toolCallId: value.tool_call_id,
          name: value.name,
          content: value.content,
          mimeType: value.mime_type,
          metadata: value.metadata,
          isError: value.is_error,
        },
      }
    }
    case 'finish': {
      const parsed = finishSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { reason, time, message, details } = parsed.data
      return { kind: 'finish', finish: { reason, time, message, details } }
    }
    case 'shell_command': {
      const parsed = shellCommandSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return {
        kind: 'shell_command',
        command: parsed.data.command,
        output: parsed.data.output,
        exitCode: parsed.data.exit_code,
      }
    }
    case 'sub_agent_report': {
      const parsed = subAgentReportSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { label, output, failed, model } = parsed.data
      return { kind: 'sub_agent_report', label, output, failed, model }
    }
    case 'compaction_stats': {
      const parsed = compactionSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'compaction', savedTokens: parsed.data.saved_tokens, keptMessages: parsed.data.kept_messages }
    }
    case 'binary': {
      const parsed = binarySchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'attachment', name: baseName(parsed.data.Path) || parsed.data.MIMEType, mimeType: parsed.data.MIMEType }
    }
    case 'image_url': {
      const parsed = imageUrlSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'attachment', name: imageName(parsed.data.url), mimeType: 'image' }
    }
    default: {
      return null
    }
  }
}

function baseName(path: string): string {
  const trimmed = path.replace(/[?#].*$/, '')
  return trimmed.slice(trimmed.lastIndexOf('/') + 1)
}

// imageName is the file name of an image URL. A pasted image arrives as a
// data: URL, which has no name to take: baseName would answer its base64
// payload, so the type alone names it.
function imageName(url: string): string {
  if (url.startsWith('data:')) {
    return 'image'
  }
  return baseName(url) || 'image'
}

// cache keeps one parsed array per message object. A streamed token makes a
// new message object, so a stale entry can never be read, and the WeakMap
// lets the old one go with it.
const cache = new WeakMap<WireMessage, MessagePart[]>()

// messageParts is the message's parts in the order they were written.
export function messageParts(message: WireMessage): MessagePart[] {
  const cached = cache.get(message)
  if (cached !== undefined) {
    return cached
  }
  const out: MessagePart[] = []
  for (const part of message.parts) {
    const parsed = parsePart(part.type, part.data)
    if (parsed !== null) {
      out.push(parsed)
    }
  }
  cache.set(message, out)
  return out
}

export function finishOf(parts: MessagePart[]): FinishPart | null {
  for (let index = parts.length - 1; index >= 0; index -= 1) {
    const part = parts[index]
    if (part.kind === 'finish') {
      return part.finish
    }
  }
  return null
}

// resultIndex maps every tool call id to its result. Results live in their
// own tool messages, after the call, so the whole log is read.
export function resultIndex(messages: WireMessage[]): Map<string, ToolResultPart> {
  const out = new Map<string, ToolResultPart>()
  for (const message of messages) {
    for (const part of messageParts(message)) {
      if (part.kind === 'tool_result') {
        out.set(part.result.toolCallId, part.result)
      }
    }
  }
  return out
}

// resultCache keeps one message's signature fragment per message object. A
// streamed token makes a new message object, so a stale fragment can never be
// read, and every other message in the log keeps the one it already had.
const resultCache = new WeakMap<WireMessage, string>()

// resultFragment hashes the results of one message. The content and the
// metadata are hashed rather than measured: a result rewritten at the same
// length still changes the key, and comparing two short digests stays cheap
// where a tool prints a whole file.
function resultFragment(message: WireMessage): string {
  const cached = resultCache.get(message)
  if (cached !== undefined) {
    return cached
  }
  const keys: string[] = []
  for (const part of messageParts(message)) {
    if (part.kind === 'tool_result') {
      const result = part.result
      keys.push(
        `${result.toolCallId}:${result.isError ? 1 : 0}:${digest(result.content)}:${digest(result.metadata)}`,
      )
    }
  }
  const fragment = keys.join(',')
  resultCache.set(message, fragment)
  return fragment
}

// resultSignature changes when a result is added, removed or rewritten. A
// view memoises the index on it, so a streamed token, which touches no
// result, keeps the index's identity and the memoised messages with it.
// Only a message object the tab has not seen is hashed: the log holds every
// result loaded so far, and a frame must not re-read them all.
export function resultSignature(messages: WireMessage[]): string {
  const fragments: string[] = []
  for (const message of messages) {
    const fragment = resultFragment(message)
    // An empty message adds no keys, and the joined signature is the same
    // whether it is skipped here or contributed nothing.
    if (fragment !== '') {
      fragments.push(fragment)
    }
  }
  return fragments.join(',')
}

// digest is FNV-1a over the string's UTF-16 code units, in base 36. A
// 32-bit collision between two texts of one log is unlikely rather than
// impossible, and all it would do is keep a stale memo key until the next
// result changes.
function digest(text: string): string {
  let hash = 0x811c9dc5
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index)
    hash = Math.imul(hash, 0x01000193)
  }
  return (hash >>> 0).toString(36)
}

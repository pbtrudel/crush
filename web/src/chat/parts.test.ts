import { describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../api/wire'
import { finishOf, messageParts, parsePart, resultIndex, resultSignature } from './parts'

function message(parts: WireMessage['parts'], id = 'm1'): WireMessage {
  return { id, role: 'assistant', session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

describe('messageParts', () => {
  it('keeps the order the message was written in', () => {
    const parts = messageParts(
      message([
        { type: 'text', data: { text: 'First I read it.' } },
        { type: 'tool_call', data: { id: 'tc-1', name: 'view', input: '{}' } },
        { type: 'text', data: { text: 'Then I changed it.' } },
        { type: 'reasoning', data: { thinking: 'Late thought.' } },
      ]),
    )
    expect(parts.map((part) => part.kind)).toEqual(['text', 'tool_call', 'text', 'reasoning'])
  })

  it('drops hidden text, unknown types and malformed parts', () => {
    const parts = messageParts(
      message([
        { type: 'text', data: { text: 'model only', hidden: true } },
        { type: 'not_a_part', data: {} },
        { type: 'tool_call', data: { name: 'bash' } },
        { type: 'text', data: { text: 'shown' } },
      ]),
    )
    expect(parts).toEqual([{ kind: 'text', text: 'shown' }])
  })

  it('answers the same array for the same message object', () => {
    const same = message([{ type: 'text', data: { text: 'hi' } }])
    expect(messageParts(same)).toBe(messageParts(same))
  })

  it('reads a tool result with its defaults', () => {
    expect(parsePart('tool_result', { tool_call_id: 'tc-1', content: 'ok' })).toEqual({
      kind: 'tool_result',
      result: { toolCallId: 'tc-1', name: '', content: 'ok', mimeType: '', metadata: '', isError: false },
    })
  })

  it('names an attachment by its path, then its type', () => {
    expect(parsePart('binary', { Path: '/tmp/shot.png', MIMEType: 'image/png' })).toEqual({
      kind: 'attachment',
      name: 'shot.png',
      mimeType: 'image/png',
    })
    expect(parsePart('image_url', { url: 'https://example.com/a.png' })).toEqual({
      kind: 'attachment',
      name: 'a.png',
      mimeType: 'image',
    })
  })

  // A pasted image arrives as a data: URL, which has no file name: baseName
  // would answer the base64 payload itself.
  it('names a data URL image by the type alone', () => {
    expect(parsePart('image_url', { url: 'data:image/png;base64,iVBORw0KGgo=' })).toEqual({
      kind: 'attachment',
      name: 'image',
      mimeType: 'image',
    })
  })
})

describe('finishOf', () => {
  it('answers the last finish part', () => {
    const parts = messageParts(message([{ type: 'finish', data: { reason: 'canceled', time: 9 } }]))
    expect(finishOf(parts)).toEqual({ reason: 'canceled', time: 9, message: '', details: '' })
    expect(finishOf([])).toBeNull()
  })
})

describe('resultIndex', () => {
  const answered = [
    message([{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}' } }], 'a1'),
    message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok', is_error: true } }], 't1'),
  ]

  it('finds a result in a later message', () => {
    expect(resultIndex(answered).get('tc-1')?.isError).toBe(true)
  })

  it('changes its signature only when a result changes', () => {
    const before = resultSignature(answered)
    const token = [...answered, message([{ type: 'text', data: { text: 'more' } }], 'a2')]
    expect(resultSignature(token)).toBe(before)
    const another = [...answered, message([{ type: 'tool_result', data: { tool_call_id: 'tc-2' } }], 't2')]
    expect(resultSignature(another)).not.toBe(before)
  })

  // A result rewritten where it stands keeps its length, so a signature
  // built from lengths alone would answer the same key and leave the
  // memoised index showing the old text.
  it('changes its signature when a result is rewritten at the same length', () => {
    const one = [message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 't1')]
    const two = [message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'no' } }], 't1')]
    expect(resultSignature(two)).not.toBe(resultSignature(one))
  })

  // The signature runs on every frame, and the log holds every result loaded
  // so far. Hashing is the expensive part, so each message object's fragment
  // is kept: a streamed token makes one new object, and only that object is
  // hashed again.
  it('hashes each message object once, and only the object that changed', () => {
    const charCodeAt = vi.spyOn(String.prototype, 'charCodeAt')
    try {
      const unchanged = [
        message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 't1'),
        message([{ type: 'text', data: { text: 'same' } }], 'a1'),
      ]
      const before = resultSignature(unchanged)
      const hashed = charCodeAt.mock.calls.length
      expect(hashed).toBeGreaterThan(0)

      expect(resultSignature(unchanged)).toBe(before)
      expect(charCodeAt.mock.calls.length).toBe(hashed)

      const rewritten = message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'no' } }], 't1')
      expect(resultSignature([rewritten, unchanged[1]])).not.toBe(before)
      expect(charCodeAt.mock.calls.length).toBeGreaterThan(hashed)
    } finally {
      charCodeAt.mockRestore()
    }
  })
})

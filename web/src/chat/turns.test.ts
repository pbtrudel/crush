import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../api/wire'
import { turnFooters } from './turns'

function entry(
  id: string,
  role: string,
  createdAt: number,
  parts: WireMessage['parts'] = [],
  model?: string,
): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: createdAt, updated_at: createdAt, model }
}

const finish = (reason: string, time: number) => ({ type: 'finish', data: { reason, time } })
const text = (value: string) => ({ type: 'text', data: { text: value } })

describe('turnFooters', () => {
  it('times a turn from its prompt to the message that ends it', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('tool_use', 105)], 'claude-sonnet'),
      entry('t1', 'tool', 106),
      entry('a2', 'assistant', 107, [finish('end_turn', 125)], 'claude-sonnet'),
    ])
    expect([...footers.entries()]).toEqual([['a2', 'claude-sonnet · 25s']])
  })

  it('ends a canceled turn too, and skips a message still streaming', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('canceled', 103)], 'm'),
      entry('u2', 'user', 200),
      entry('a2', 'assistant', 201, [], 'm'),
    ])
    expect([...footers.keys()]).toEqual(['a1'])
  })

  it('leaves out what it does not know', () => {
    const footers = turnFooters([entry('a1', 'assistant', 101, [finish('end_turn', 0)])])
    expect(footers.size).toBe(0)
  })

  // The user message that opened the turn lives in a page the tab has not
  // loaded, so the turn is timed from the first message the log does carry.
  it('times a turn whose prompt is in an older page', () => {
    const footers = turnFooters([entry('a1', 'assistant', 500, [finish('end_turn', 545)], 'm')])
    expect([...footers.entries()]).toEqual([['a1', 'm · 45s']])
  })

  it('gives a turn that is still running no footer', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [text('Writing')], 'm'),
    ])
    expect(footers.size).toBe(0)
  })

  // A delta on the streaming message makes a new message object and a new
  // log array; the settled turn's footer must stay the same string, or the
  // memoised message view renders again on every token.
  it('keeps an earlier turn’s footer when the last message streams', () => {
    const settled = [entry('u1', 'user', 100), entry('a1', 'assistant', 101, [finish('end_turn', 125)], 'm')]
    const before = [...settled, entry('u2', 'user', 200), entry('a2', 'assistant', 201, [text('Wri')], 'm')]
    const after = [...settled, entry('u2', 'user', 200), entry('a2', 'assistant', 201, [text('Writing')], 'm')]
    expect(turnFooters(before).get('a1')).toBe('m · 25s')
    expect(turnFooters(after).get('a1')).toBe(turnFooters(before).get('a1'))
    expect(turnFooters(after).has('a2')).toBe(false)
  })
})

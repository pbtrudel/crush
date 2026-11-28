import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../api/wire'
import { turnFooters } from './turns'

function entry(
  id: string,
  role: string,
  createdAt: number,
  parts: WireMessage['parts'] = [],
  extra: Partial<WireMessage> = {},
): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: createdAt, updated_at: createdAt, ...extra }
}

const finish = (reason: string, time: number) => ({ type: 'finish', data: { reason, time } })
const text = (value: string) => ({ type: 'text', data: { text: value } })

describe('turnFooters', () => {
  it('times a turn from its prompt to the message that ends it', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('tool_use', 105)], { model: 'claude-sonnet' }),
      entry('t1', 'tool', 106),
      entry('a2', 'assistant', 107, [finish('end_turn', 125)], { model: 'claude-sonnet' }),
    ])
    expect([...footers.entries()]).toEqual([['a2', 'claude-sonnet · 25s']])
  })

  it('ends a canceled turn too, and skips a message still streaming', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('canceled', 103)], { model: 'm' }),
      entry('u2', 'user', 200),
      entry('a2', 'assistant', 201, [], { model: 'm' }),
    ])
    expect([...footers.keys()]).toEqual(['a1'])
  })

  it('leaves out what it does not know', () => {
    const footers = turnFooters([entry('a1', 'assistant', 101, [finish('end_turn', 0)])])
    expect(footers.size).toBe(0)
  })

  // The user message that opened the turn lives in a page the tab has not
  // loaded, so the prompt is unknown and the footer names the model alone.
  it('names the model alone when the prompt is in an older page', () => {
    const footers = turnFooters([entry('a1', 'assistant', 500, [finish('end_turn', 545)], { model: 'm' })])
    expect([...footers.entries()]).toEqual([['a1', 'm']])
  })

  // Neither a prompt nor a duration is known here: the user message is on
  // an older page, the tool step does not end the turn, and the step that
  // does cannot be timed.
  it('names the model alone for a turn that ends on its second step', () => {
    const footers = turnFooters([
      entry('a1', 'assistant', 100, [finish('tool_use', 105)], { model: 'm' }),
      entry('t1', 'tool', 106),
      entry('a2', 'assistant', 107, [finish('end_turn', 125)], { model: 'm' }),
    ])
    expect([...footers.entries()]).toEqual([['a2', 'm']])
  })

  it('gives a turn that is still running no footer', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [text('Writing')], { model: 'm' }),
    ])
    expect(footers.size).toBe(0)
  })

  // A delta on the streaming message makes a new message object and a new
  // log array; the settled turn's footer must stay the same string, or the
  // memoised message view renders again on every token.
  it('keeps an earlier turn’s footer when the last message streams', () => {
    const settled = [entry('u1', 'user', 100), entry('a1', 'assistant', 101, [finish('end_turn', 125)], { model: 'm' })]
    const before = [...settled, entry('u2', 'user', 200), entry('a2', 'assistant', 201, [text('Wri')], { model: 'm' })]
    const after = [...settled, entry('u2', 'user', 200), entry('a2', 'assistant', 201, [text('Writing')], { model: 'm' })]
    expect(turnFooters(before).get('a1')).toBe('m · 25s')
    expect(turnFooters(after).get('a1')).toBe(turnFooters(before).get('a1'))
    expect(turnFooters(after).has('a2')).toBe(false)
  })

  // The gateway sends the display name beside the model id when it has one;
  // that name is what the footer reads, the id is the fallback.
  it('prefers the model display name over the id', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('end_turn', 125)], {
        model: 'claude-sonnet',
        model_name: 'Claude Sonnet 4',
      }),
    ])
    expect(footers.get('a1')).toBe('Claude Sonnet 4 · 25s')
  })

  it('falls back to the model id when there is no display name', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('end_turn', 125)], { model: 'claude-sonnet' }),
    ])
    expect(footers.get('a1')).toBe('claude-sonnet · 25s')
  })

  // A compaction summary renders as its own status line, the way the TUI
  // does it (internal/ui/chat/messages.go ShouldShowAssistantInfo): a model
  // footer under a summary nobody reads full screen would be noise.
  it('gives a compaction summary no footer', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('end_turn', 125)], { model: 'm', is_summary_message: true }),
    ])
    expect(footers.size).toBe(0)
  })
})

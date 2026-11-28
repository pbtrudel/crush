import { describe, expect, it } from 'vitest'

import type { FinishPart, ToolResultPart } from './parts'
import { interruptedResultPrefix, isTurnActive, toolStatus } from './toolStatus'

function result(overrides: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'bash', content: 'ok', mimeType: '', metadata: '', isError: false, ...overrides }
}

function finish(reason: string): FinishPart {
  return { reason, time: 1, message: '', details: '' }
}

describe('toolStatus', () => {
  it('is done when a result exists, and failed when it is an error', () => {
    expect(toolStatus(result(), null, true)).toBe('done')
    expect(toolStatus(result({ isError: true, content: 'boom' }), null, true)).toBe('error')
  })

  it('reads the settle sentence and the interrupted flag as interrupted', () => {
    expect(toolStatus(result({ isError: true, content: `${interruptedResultPrefix} and did not…` }), null, false)).toBe(
      'interrupted',
    )
    expect(toolStatus(result({ metadata: '{"interrupted":true}' }), null, false)).toBe('interrupted')
  })

  // settleTurn marks a call its turn never answered with is_error and the
  // agent's interrupted kind in the metadata, and its content is the
  // turn-end sentence, not the older prefix (internal/agent/settle.go,
  // internal/agent/tools/error_kind.go). The call was cut off, not failed.
  it('reads the settled turn-end result as interrupted, not failed', () => {
    const settled = result({
      isError: true,
      content: 'Error: user cancelled assistant tool calling',
      metadata: '{"error_kind":"interrupted"}',
    })
    expect(toolStatus(settled, null, false)).toBe('interrupted')
    expect(toolStatus(settled, null, true)).toBe('interrupted')
  })

  it('runs only while its turn is live', () => {
    expect(toolStatus(undefined, null, true)).toBe('running')
    expect(toolStatus(undefined, finish('tool_use'), true)).toBe('running')
    expect(toolStatus(undefined, null, false)).toBe('interrupted')
  })

  it('is interrupted by a canceled or failed turn even while the session runs', () => {
    expect(toolStatus(undefined, finish('canceled'), true)).toBe('interrupted')
    expect(toolStatus(undefined, finish('error'), true)).toBe('interrupted')
  })
})

describe('isTurnActive', () => {
  it('counts a running session and one waiting for the person', () => {
    expect(isTurnActive('running')).toBe(true)
    expect(isTurnActive('pending_interaction')).toBe(true)
    expect(isTurnActive('queued')).toBe(false)
    expect(isTurnActive('idle')).toBe(false)
    expect(isTurnActive(undefined)).toBe(false)
  })
})
